package kernel

import (
	"encoding/json"
	"fmt"
	"path"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// blob —— 大字节存储；artifact 指向它。
//
// 事件流必须保持小而统一。agent 产出文件（报告、表格、图片）时，字节进 BlobStore，
// 事件日志只记一条指针（filename/version/uri/mime/size）作为 ARTIFACT_WRITTEN 事件。
// 所以 artifact 和状态、Trace 一样是事件流的投影：
//
//	replay the pointer events  -> the artifact library (what files, which versions)
//	call BlobStore.Load(uri)   -> the bytes themselves
//
// 版本号由 store 负责：同名文件每次保存取"现存最高版本 + 1"，而非计数，因此删掉中间
// 版本不会让下次保存覆写仍在的文件。文件名归约为 basename，所以 "../x" 这类名字永远
// 无法寻址到 run 目录之外。

// extMime 是最小的扩展名 -> MIME 表，按固定顺序存放，使猜测结果确定（Go map 迭代无序）。
var extMime = []struct{ ext, mime string }{
	{".md", "text/markdown"},
	{".txt", "text/plain"},
	{".json", "application/json"},
	{".csv", "text/csv"},
	{".html", "text/html"},
	{".png", "image/png"},
	{".jpg", "image/jpeg"},
	{".jpeg", "image/jpeg"},
	{".pdf", "application/pdf"},
}

func guessMime(filename string, data any) string {
	name := strings.ToLower(filename)
	for _, e := range extMime {
		if strings.HasSuffix(name, e.ext) {
			return e.mime
		}
	}
	if _, ok := data.(string); ok {
		return "text/plain"
	}
	return "application/octet-stream"
}

// asBytes 把数据归一化为字节；结构化值转成缩进 JSON，使制品在进程外也可移植、可读。
func asBytes(data any) []byte {
	switch v := data.(type) {
	case string:
		return []byte(v)
	case []byte:
		return v
	default:
		b, err := json.MarshalIndent(data, "", "  ")
		if err != nil {
			return []byte(fmt.Sprintf("%v", data))
		}
		return b
	}
}

// nextVersion 返回"现存最高版本 + 1"（取 max，绝不计数）。内存与本地 store 共用
// 这一条规则，不会漂移。
func nextVersion(used []int) int {
	m := 0
	for _, v := range used {
		if v > m {
			m = v
		}
	}
	return m + 1
}

// versionsPresent 从以 prefix（".../{filename}.v"）开头的现存 uri 中抽出版本号，
// 即 nextVersion 折叠的事实。
func versionsPresent(uris []string, prefix string) []int {
	var out []int
	for _, u := range uris {
		if strings.HasPrefix(u, prefix) {
			if n, err := strconv.Atoi(u[len(prefix):]); err == nil {
				out = append(out, n)
			}
		}
	}
	return out
}

// BlobStore 是存储端口（DIP）：Save 分配下一个版本和 uri，Load 取回，Delete 删除。
type BlobStore interface {
	Save(runID, filename string, data any, mime string) (map[string]any, error)
	Load(uri string) ([]byte, error)
	Delete(uri string) error
}

// InMemoryBlobStore 用 map 存字节；下一版本从现存 uri 折叠得出。零副作用的默认实现
// （与内存日志同一思路）。
type InMemoryBlobStore struct {
	mu    sync.Mutex
	blobs map[string][]byte
}

// NewInMemoryBlobStore 构建一个空 store。
func NewInMemoryBlobStore() *InMemoryBlobStore {
	return &InMemoryBlobStore{blobs: map[string][]byte{}}
}

func (s *InMemoryBlobStore) Save(runID, filename string, data any, mime string) (map[string]any, error) {
	filename = path.Base(filename) // 绝不寻址到 run 目录之外
	body := asBytes(data)
	if mime == "" {
		mime = guessMime(filename, data)
	}
	prefix := fmt.Sprintf("%s/%s.v", runID, filename)
	s.mu.Lock()
	uris := make([]string, 0, len(s.blobs))
	for u := range s.blobs {
		uris = append(uris, u)
	}
	version := nextVersion(versionsPresent(uris, prefix))
	uri := fmt.Sprintf("%s%d", prefix, version)
	s.blobs[uri] = body
	s.mu.Unlock()
	return map[string]any{
		"filename": filename,
		"version":  version,
		"uri":      uri,
		"mime":     mime,
		"size":     len(body),
	}, nil
}

func (s *InMemoryBlobStore) Load(uri string) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, ok := s.blobs[uri]
	if !ok {
		return nil, fmt.Errorf("blob not found: %s", uri)
	}
	return b, nil
}

func (s *InMemoryBlobStore) Delete(uri string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.blobs, uri)
	return nil
}

// —— 投影：artifact 库是指针事件的折叠 ——

// ArtifactsFromEvents 折叠出 filename -> 全部版本指针，按保存顺序。
//
// 折叠断言每个文件名的版本号沿事件流严格递增。保存的事实在 store 分配完版本号后才
// 提交，因此版本回退（或重复）意味着 store 顺序与提交顺序不一致——两个写者争抢同一
// 文件名，或删除后版本号被复用——此时"最新"会悄悄说谎。折叠拒绝认可它：在这里大声
// 报错，每个读方都自动继承这项检查。
func ArtifactsFromEvents(events []Event) (map[string][]map[string]any, error) {
	out := map[string][]map[string]any{}
	last := map[string]int{}
	for _, ev := range events {
		if ev.Kind != EvArtifactWritten {
			continue
		}
		pointer := copyMap(ev.Data)
		name, _ := pointer["filename"].(string)
		version := toInt(pointer["version"])
		if prev, ok := last[name]; ok && version <= prev {
			return nil, fmt.Errorf(
				"artifact %q: version %d arrives after %d — versions must strictly increase "+
					"along the stream (two writers racing one filename, or a number reused)",
				name, version, prev)
		}
		last[name] = version
		out[name] = append(out[name], pointer)
	}
	return out, nil
}

// LatestArtifacts 折叠出 filename -> 最新版本指针（"Files" 面板）。
func LatestArtifacts(events []Event) (map[string]map[string]any, error) {
	byName, err := ArtifactsFromEvents(events)
	if err != nil {
		return nil, err
	}
	out := make(map[string]map[string]any, len(byName))
	for name, versions := range byName {
		out[name] = versions[len(versions)-1]
	}
	return out, nil
}

// ResolveArtifact 从事件流折叠出 filename 的指针（version <= 0 取最新，否则取该
// 版本）——artifact 法则的读侧。
func ResolveArtifact(events []Event, filename string, version int) (map[string]any, error) {
	byName, err := ArtifactsFromEvents(events)
	if err != nil {
		return nil, err
	}
	versions := byName[filename]
	if len(versions) == 0 {
		return nil, fmt.Errorf("no artifact named %q in this run", filename)
	}
	if version <= 0 {
		return versions[len(versions)-1], nil
	}
	for _, pointer := range versions {
		if toInt(pointer["version"]) == version {
			return pointer, nil
		}
	}
	return nil, fmt.Errorf("artifact %q has no version %d", filename, version)
}

// sortedInts 是个极小的辅助函数，让报告里的排序确定。
func sortedInts(in []int) []int {
	out := make([]int, len(in))
	copy(out, in)
	sort.Ints(out)
	return out
}
