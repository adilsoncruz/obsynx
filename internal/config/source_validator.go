package config

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// DiscoveredSymbol represents a function or method found in a project's source code.
type DiscoveredSymbol struct {
	File       string
	Class      string // Class name, receiver type, or module name
	Method     string
	Params     []string
	ParamTypes []string
	ReturnType string
}

// SourceIndex holds all functions, classes, and struct/data-class/object fields found in a project directory.
type SourceIndex struct {
	Symbols    []DiscoveredSymbol
	TypeFields map[string]map[string]bool // typeName -> fieldName -> true
	AllFields  map[string]bool            // fallback union of object keys/fields returned in dynamic languages
}

// ValidateProjectSource verifies that every declared method, class, input argument (arg.*),
// and return attribute (return.*) in `svc` exists in `projectDir` and is unambiguous.
func ValidateProjectSource(projectDir string, svc ServiceConfig) error {
	info, err := os.Stat(projectDir)
	if err != nil || !info.IsDir() {
		return nil
	}

	idx, err := indexProjectSource(projectDir)
	if err != nil {
		return err
	}
	if len(idx.Symbols) == 0 {
		return nil
	}

	for _, rule := range svc.Instrumentation.Methods {
		sym, err := resolveUnambiguousSymbol(svc.Name, rule, idx)
		if err != nil {
			return err
		}
		if err := validateRuleExpressionsAgainstSymbol(svc.Name, rule, sym, idx); err != nil {
			return err
		}
	}

	return nil
}

func resolveUnambiguousSymbol(serviceName string, rule MethodRule, idx SourceIndex) (DiscoveredSymbol, error) {
	var matches []DiscoveredSymbol
	targetClass := strings.TrimSpace(rule.Class)

	for _, sym := range idx.Symbols {
		if sym.Method != rule.Method {
			continue
		}
		if targetClass == "" {
			matches = append(matches, sym)
			continue
		}
		if matchesClassSelector(sym, targetClass) {
			matches = append(matches, sym)
		}
	}

	if len(matches) == 0 {
		if targetClass != "" {
			return DiscoveredSymbol{}, fmt.Errorf(
				"service %s: method %q in class/module %q not found in project source code",
				serviceName, rule.Method, targetClass,
			)
		}
		return DiscoveredSymbol{}, fmt.Errorf(
			"service %s: method %q not found in project source code",
			serviceName, rule.Method,
		)
	}

	if len(matches) > 1 {
		var locations []string
		for _, m := range matches {
			qualifier := m.Class
			if qualifier == "" {
				qualifier = filepath.Base(m.File)
			}
			locations = append(locations, fmt.Sprintf("%s.%s (%s)", qualifier, m.Method, filepath.Base(m.File)))
		}
		return DiscoveredSymbol{}, fmt.Errorf(
			"service %s: ambiguous method %q found in multiple classes/modules [%s]; specify 'class' in obsynx.yaml to disambiguate",
			serviceName, rule.Method, strings.Join(locations, ", "),
		)
	}

	return matches[0], nil
}

func matchesClassSelector(sym DiscoveredSymbol, selector string) bool {
	if sym.Class == selector || strings.HasSuffix(selector, "."+sym.Class) {
		return true
	}
	baseFile := strings.TrimSuffix(filepath.Base(sym.File), filepath.Ext(sym.File))
	return baseFile == selector || filepath.Base(sym.File) == selector
}

func validateRuleExpressionsAgainstSymbol(
	serviceName string,
	rule MethodRule,
	sym DiscoveredSymbol,
	idx SourceIndex,
) error {
	for _, metric := range rule.Metrics {
		if err := checkSourceExpr(serviceName, rule.Method, metric.ValueFrom, sym, idx); err != nil {
			return err
		}
		for _, expr := range metric.AttributesFrom {
			if err := checkSourceExpr(serviceName, rule.Method, expr, sym, idx); err != nil {
				return err
			}
		}
	}
	return nil
}

func checkSourceExpr(
	serviceName, methodName, expr string,
	sym DiscoveredSymbol,
	idx SourceIndex,
) error {
	trimmed := strings.TrimSpace(expr)
	if trimmed == "" || trimmed == "1" || trimmed == "duration_ms" || trimmed == "return" {
		return nil
	}

	if strings.HasPrefix(trimmed, "arg.") {
		key := strings.TrimPrefix(trimmed, "arg.")
		if argIdx, err := strconv.Atoi(key); err == nil {
			if argIdx < 0 || argIdx >= len(sym.Params) {
				return fmt.Errorf(
					"service %s method %s: %q is out of bounds (function has %d parameter(s): [%s])",
					serviceName, methodName, trimmed, len(sym.Params), strings.Join(sym.Params, ", "),
				)
			}
			return nil
		}

		// Named parameter or field inside the first parameter struct/class (e.g. req.amount -> arg.amount)
		for _, p := range sym.Params {
			if p == key || toSnakeCase(p) == toSnakeCase(key) {
				return nil
			}
		}
		for _, pt := range sym.ParamTypes {
			if fields, ok := idx.TypeFields[pt]; ok {
				if hasFieldCaseInsensitive(fields, key) {
					return nil
				}
			}
		}
		return fmt.Errorf(
			"service %s method %s: argument or input field %q not found in parameters [%s]",
			serviceName, methodName, trimmed, strings.Join(sym.Params, ", "),
		)
	}

	if strings.HasPrefix(trimmed, "return.") {
		field := strings.TrimPrefix(trimmed, "return.")
		if sym.ReturnType != "" {
			if fields, ok := idx.TypeFields[sym.ReturnType]; ok {
				if hasFieldCaseInsensitive(fields, field) {
					return nil
				}
				return fmt.Errorf(
					"service %s method %s: return field %q not found on return type %s",
					serviceName, methodName, trimmed, sym.ReturnType,
				)
			}
		}
		if len(idx.AllFields) > 0 && !hasFieldCaseInsensitive(idx.AllFields, field) {
			return fmt.Errorf(
				"service %s method %s: return attribute %q not found in function return object",
				serviceName, methodName, trimmed,
			)
		}
	}

	return nil
}

func hasFieldCaseInsensitive(fields map[string]bool, target string) bool {
	if fields[target] {
		return true
	}
	targetSnake := toSnakeCase(target)
	for k := range fields {
		if strings.EqualFold(k, target) || toSnakeCase(k) == targetSnake {
			return true
		}
	}
	return false
}

func toSnakeCase(s string) string {
	var out []rune
	for i, r := range s {
		if r >= 'A' && r <= 'Z' {
			if i > 0 {
				out = append(out, '_')
			}
			out = append(out, r+('a'-'A'))
		} else {
			out = append(out, r)
		}
	}
	return string(out)
}

func indexProjectSource(projectDir string) (SourceIndex, error) {
	idx := SourceIndex{
		TypeFields: make(map[string]map[string]bool),
		AllFields:  make(map[string]bool),
	}

	err := filepath.WalkDir(projectDir, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			base := d.Name()
			if base == "node_modules" || base == ".gradle" || base == "build" || base == ".git" || base == "bin" || base == "obj" {
				return filepath.SkipDir
			}
			return nil
		}

		switch filepath.Ext(path) {
		case ".go":
			indexGoFile(path, &idx)
		case ".kt":
			indexKotlinFile(path, &idx)
		case ".js":
			indexJSFile(path, &idx)
		case ".py":
			indexPythonFile(path, &idx)
		case ".cs":
			indexCSharpFile(path, &idx)
		}
		return nil
	})
	return idx, err
}

func indexGoFile(path string, idx *SourceIndex) {
	fset := token.NewFileSet()
	node, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		return
	}

	for _, decl := range node.Decls {
		switch d := decl.(type) {
		case *ast.GenDecl:
			for _, spec := range d.Specs {
				ts, ok := spec.(*ast.TypeSpec)
				if !ok {
					continue
				}
				st, ok := ts.Type.(*ast.StructType)
				if !ok {
					continue
				}
				fields := make(map[string]bool)
				for _, f := range st.Fields.List {
					for _, name := range f.Names {
						fields[name.Name] = true
						idx.AllFields[name.Name] = true
					}
					if f.Tag != nil {
						if jsonName := extractJSONTag(f.Tag.Value); jsonName != "" {
							fields[jsonName] = true
							idx.AllFields[jsonName] = true
						}
					}
				}
				idx.TypeFields[ts.Name.Name] = fields
			}

		case *ast.FuncDecl:
			sym := DiscoveredSymbol{
				File:   path,
				Method: d.Name.Name,
			}
			if d.Recv != nil && len(d.Recv.List) > 0 {
				sym.Class = exprTypeName(d.Recv.List[0].Type)
			}
			if d.Type.Params != nil {
				for _, p := range d.Type.Params.List {
					pType := exprTypeName(p.Type)
					if len(p.Names) == 0 {
						sym.Params = append(sym.Params, "_")
						sym.ParamTypes = append(sym.ParamTypes, pType)
					}
					for _, n := range p.Names {
						sym.Params = append(sym.Params, n.Name)
						sym.ParamTypes = append(sym.ParamTypes, pType)
					}
				}
			}
			if d.Type.Results != nil && len(d.Type.Results.List) > 0 {
				sym.ReturnType = exprTypeName(d.Type.Results.List[0].Type)
			}
			idx.Symbols = append(idx.Symbols, sym)
		}
	}
}

func exprTypeName(expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.StarExpr:
		return exprTypeName(t.X)
	default:
		return ""
	}
}

func extractJSONTag(tagLiteral string) string {
	re := regexp.MustCompile(`json:"([^",]+)`)
	m := re.FindStringSubmatch(tagLiteral)
	if len(m) > 1 {
		return m[1]
	}
	return ""
}

var (
	ktClassRe     = regexp.MustCompile(`(?m)^\s*(?:data\s+)?class\s+([A-Za-z0-9_]+)\s*(?:\(([^)]*)\))?`)
	ktFunRe       = regexp.MustCompile(`(?m)^\s*fun\s+([A-Za-z0-9_]+)\s*\(([^)]*)\)\s*(?::\s*([A-Za-z0-9_<>]+))?`)
	ktPropRe      = regexp.MustCompile(`(?:val|var)\s+([A-Za-z0-9_]+)\s*:\s*([A-Za-z0-9_]+)`)
	jsFunRe       = regexp.MustCompile(`(?m)function\s+([A-Za-z0-9_]+)\s*\(([^)]*)\)`)
	jsObjKeyRe    = regexp.MustCompile(`(?m)^\s*([a-zA-Z_][a-zA-Z0-9_]*)\s*(?::|,|$)`)
	pyDefRe       = regexp.MustCompile(`(?m)^(\s*)def\s+([A-Za-z0-9_]+)\s*\(([^)]*)\)`)
	pyClassRe     = regexp.MustCompile(`(?m)^class\s+([A-Za-z0-9_]+)`)
	pyDictKeyRe   = regexp.MustCompile(`["']([a-zA-Z_][a-zA-Z0-9_]*)["']\s*:`)
	csRecordRe    = regexp.MustCompile(`(?m)^\s*(?:public|internal|private)?\s*(?:readonly\s+)?record\s+(?:class\s+|struct\s+)?([A-Za-z0-9_]+)\s*\(([^)]*)\)`)
	csClassRe     = regexp.MustCompile(`(?m)^\s*(?:public|internal|private)?\s*(?:static\s+|abstract\s+|sealed\s+)?class\s+([A-Za-z0-9_]+)`)
	csMethodRe    = regexp.MustCompile(`(?m)^\s*(?:public|internal|private|protected)?\s*(?:static\s+|async\s+|virtual\s+|override\s+)*([A-Za-z0-9_<>?]+)\s+([A-Za-z0-9_]+)\s*\(([^)]*)\)`)
)

func indexKotlinFile(path string, idx *SourceIndex) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	text := string(data)

	// Index data classes and their constructor properties
	for _, m := range ktClassRe.FindAllStringSubmatch(text, -1) {
		clsName := m[1]
		ctorBody := m[2]
		fields := make(map[string]bool)
		for _, pm := range ktPropRe.FindAllStringSubmatch(ctorBody, -1) {
			fields[pm[1]] = true
			idx.AllFields[pm[1]] = true
		}
		if len(fields) > 0 {
			idx.TypeFields[clsName] = fields
		}
	}

	// Track current enclosing class line-by-line
	var currentClass string
	for _, line := range strings.Split(text, "\n") {
		if cm := ktClassRe.FindStringSubmatch(line); len(cm) > 1 {
			currentClass = cm[1]
		}
		if fm := ktFunRe.FindStringSubmatch(line); len(fm) > 1 {
			fnName := fm[1]
			rawParams := fm[2]
			retType := fm[3]
			var params, paramTypes []string
			for _, part := range strings.Split(rawParams, ",") {
				part = strings.TrimSpace(part)
				if part == "" {
					continue
				}
				name, typ, ok := strings.Cut(part, ":")
				if ok {
					params = append(params, strings.TrimSpace(name))
					paramTypes = append(paramTypes, strings.TrimSpace(typ))
				}
			}
			idx.Symbols = append(idx.Symbols, DiscoveredSymbol{
				File:       path,
				Class:      currentClass,
				Method:     fnName,
				Params:     params,
				ParamTypes: paramTypes,
				ReturnType: strings.TrimSpace(retType),
			})
		}
	}
}

func indexJSFile(path string, idx *SourceIndex) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	text := string(data)
	moduleName := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))

	for _, fm := range jsFunRe.FindAllStringSubmatch(text, -1) {
		fnName := fm[1]
		var params []string
		for _, p := range strings.Split(fm[2], ",") {
			p = strings.TrimSpace(strings.Split(p, "=")[0])
			if p != "" {
				params = append(params, p)
			}
		}
		idx.Symbols = append(idx.Symbols, DiscoveredSymbol{
			File:   path,
			Class:  moduleName,
			Method: fnName,
			Params: params,
		})
	}

	for _, km := range jsObjKeyRe.FindAllStringSubmatch(text, -1) {
		key := strings.TrimSpace(km[1])
		if key != "return" && key != "const" && key != "let" && key != "function" {
			idx.AllFields[key] = true
		}
	}
}

func indexPythonFile(path string, idx *SourceIndex) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	text := string(data)
	moduleName := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))

	var currentClass string
	for _, line := range strings.Split(text, "\n") {
		if cm := pyClassRe.FindStringSubmatch(line); len(cm) > 1 {
			currentClass = cm[1]
			continue
		}
		if dm := pyDefRe.FindStringSubmatch(line); len(dm) > 2 {
			indent := dm[1]
			fnName := dm[2]
			cls := moduleName
			if len(indent) > 0 && currentClass != "" {
				cls = currentClass
			} else {
				currentClass = ""
			}
			var params []string
			for _, p := range strings.Split(dm[3], ",") {
				p = strings.TrimSpace(strings.Split(strings.Split(p, ":")[0], "=")[0])
				if p != "" && p != "self" && p != "cls" {
					params = append(params, p)
				}
			}
			idx.Symbols = append(idx.Symbols, DiscoveredSymbol{
				File:   path,
				Class:  cls,
				Method: fnName,
				Params: params,
			})
		}
	}

	for _, km := range pyDictKeyRe.FindAllStringSubmatch(text, -1) {
		idx.AllFields[km[1]] = true
	}
}

func indexCSharpFile(path string, idx *SourceIndex) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	text := string(data)

	// Index records and their positional parameters (e.g. record PaymentResponse(string Status, decimal Amount...))
	for _, m := range csRecordRe.FindAllStringSubmatch(text, -1) {
		recordName := m[1]
		paramList := m[2]
		fields := make(map[string]bool)
		for _, part := range strings.Split(paramList, ",") {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			tokens := strings.Fields(part)
			if len(tokens) >= 2 {
				name := tokens[len(tokens)-1]
				name = strings.Trim(name, ";, \t\r\n")
				fields[name] = true
				idx.AllFields[name] = true
			}
		}
		if len(fields) > 0 {
			idx.TypeFields[recordName] = fields
		}
	}

	// Index classes and methods
	var currentClass string
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if cm := csClassRe.FindStringSubmatch(trimmed); len(cm) > 1 {
			currentClass = cm[1]
			continue
		}
		if mm := csMethodRe.FindStringSubmatch(trimmed); len(mm) > 3 {
			retType := mm[1]
			fnName := mm[2]
			rawParams := mm[3]
			if fnName == "if" || fnName == "for" || fnName == "while" || fnName == "switch" || fnName == "catch" {
				continue
			}
			var params, paramTypes []string
			for _, part := range strings.Split(rawParams, ",") {
				part = strings.TrimSpace(part)
				if part == "" {
					continue
				}
				tokens := strings.Fields(part)
				if len(tokens) >= 2 {
					typ := tokens[0]
					name := tokens[len(tokens)-1]
					name = strings.Trim(name, ";, \t\r\n")
					params = append(params, name)
					paramTypes = append(paramTypes, typ)
				}
			}
			idx.Symbols = append(idx.Symbols, DiscoveredSymbol{
				File:       path,
				Class:      currentClass,
				Method:     fnName,
				Params:     params,
				ParamTypes: paramTypes,
				ReturnType: retType,
			})
		}
	}
}
