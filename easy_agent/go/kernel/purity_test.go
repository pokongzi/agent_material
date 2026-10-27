package kernel

// 纯净性守卫：内核只允许依赖 Go 标准库。
//
// 这把"机制在内、策略在外、内核不认任何厂商 SDK"变成可执行约束：
// 在内核里 import 第三方模块，CI 立即变红。（test_purity.py 的 Go 对应版本。）

import (
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"
)

func TestKernelHasNoThirdPartyImports(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	offenders := map[string][]string{}
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue // 守卫对象是交付的内核，不是它的测试
		}
		f, err := parser.ParseFile(fset, file, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, imp := range f.Imports {
			path := strings.Trim(imp.Path.Value, `"`)
			if isThirdPartyImport(path) {
				offenders[file] = append(offenders[file], path)
			}
		}
	}
	if len(offenders) > 0 {
		t.Fatalf("kernel has third-party dependencies: %v", offenders)
	}
}

// isThirdPartyImport 判断 import 路径是否离开标准库。
// 标准库包的第一段路径不含点（"fmt"、"encoding/json"、"crypto/rand"），
// 模块路径则必含点（"github.com/..."、"gopkg.in/..."）。这与 cmd/go 的区分规则一致。
func isThirdPartyImport(importPath string) bool {
	first := importPath
	if i := strings.IndexByte(importPath, '/'); i >= 0 {
		first = importPath[:i]
	}
	return strings.Contains(first, ".")
}
