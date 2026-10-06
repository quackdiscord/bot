// Package contract generates the HTTP API contract,
// contracts/http/openapi.yaml, from the API's route table.
//
// Every route mounted by api.Server records an api.Doc naming the Go types
// its handler reads and writes. Build reflects those types into an OpenAPI
// 3.1 document with github.com/swaggest/openapi-go, so the contract follows
// the code: JSON names, omitempty, and nullability come from the struct tags
// and Go types the handlers actually encode. cmd/openapi writes the file;
// tests check it is current and covers every mounted route.
package contract

import (
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"

	"github.com/quackdiscord/bot/internal/api"
	"github.com/swaggest/jsonschema-go"
	"github.com/swaggest/openapi-go"
	"github.com/swaggest/openapi-go/openapi31"
)

const (
	modulePath = "github.com/quackdiscord/bot"

	// Security scheme names.
	sessionCookie = "sessionCookie"
	sessionBearer = "sessionBearer"
	opsKey        = "opsKey"
	metricsKey    = "metricsKey"
)

const description = `The Quack dashboard API.

Every error status (400 and up) carries the error envelope, whatever the
handler wrote. Unknown paths and wrong methods are 404s.

The dashboard authenticates with the HttpOnly session cookie set by Discord
sign-in (quack_session by default). Writes made with the cookie must come from
an allowed Origin and echo the CSRF cookie (quack_csrf by default, also
returned as csrf_token by /auth/me) in X-CSRF-Token. A bearer session token is
accepted instead of the cookie and skips the CSRF check.

Writes marked with an Idempotency-Key header are safe to retry with the same
key: a completed request is replayed with Idempotency-Replayed: true, one
still running is a 409 with Retry-After, and reusing a key for a different
request is a 409.

This file is generated from the Go route table. Do not edit it; run
"go generate ./..." from apps/backend.`

// Build returns the OpenAPI document for routes, which should come from
// app.Routes.
func Build(routes []api.Route) (*openapi31.Spec, error) {
	moduleRoot, err := findModuleRoot()
	if err != nil {
		return nil, err
	}
	enums := enumScanner{root: moduleRoot, values: map[reflect.Type][]any{}}
	responseTypes := map[reflect.Type]bool{}
	collectStructs(reflect.TypeOf(api.ErrorBody()), responseTypes)
	for _, route := range routes {
		collectStructs(reflect.TypeOf(route.Doc.Response), responseTypes)
		for _, also := range route.Doc.Also {
			collectStructs(reflect.TypeOf(also.Body), responseTypes)
		}
	}

	r := openapi31.NewReflector()
	r.Spec.Info.
		WithTitle("Quack HTTP API").
		WithVersion("5").
		WithDescription(description)
	r.Spec.SetAPIKeySecurity(sessionCookie, "quack_session", openapi.InCookie,
		"The session cookie set by Discord sign-in. Its name is configurable (auth.session_cookie_name).")
	r.Spec.SetHTTPBearerTokenSecurity(sessionBearer, "",
		"The session ID as a bearer token, for callers that are not browsers.")
	r.Spec.SetAPIKeySecurity(opsKey, "X-Quack-Ops-Key", openapi.InHeader, "The operator key (api.ops_token).")
	r.Spec.SetAPIKeySecurity(metricsKey, "X-Quack-Metrics-Key", openapi.InHeader, "The metrics key (api.metrics_token).")

	var errs []error
	r.DefaultOptions = append(r.DefaultOptions,
		// encoding/json encodes untagged exported fields, so the schema must too.
		jsonschema.ProcessWithoutTags,
		// A nil pointer to a shared type encodes as null, so say so where
		// the type is used rather than in its definition.
		func(rc *jsonschema.ReflectContext) { rc.EnvelopNullability = true },
		jsonschema.InterceptSchema(func(p jsonschema.InterceptSchemaParams) (bool, error) {
			if !p.Processed {
				return false, nil
			}
			t := p.Value.Type()
			if t.Kind() == reflect.String && t.PkgPath() != "" {
				values, err := enums.enum(t)
				if err != nil {
					errs = append(errs, err)
				}
				if len(values) > 0 {
					p.Schema.Enum = values
				}
			}
			if t.Kind() == reflect.Struct && responseTypes[t] {
				p.Schema.Required = alwaysPresent(t)
			}
			return false, nil
		}),
		// A `type` tag documents a string field that carries another type,
		// such as a query limit read as a string and validated later.
		jsonschema.InterceptProp(func(p jsonschema.InterceptPropParams) error {
			if !p.Processed {
				return nil
			}
			if typ := p.Field.Tag.Get("type"); typ != "" {
				p.PropertySchema.Type = nil
				p.PropertySchema.WithType(jsonschema.SimpleType(typ).Type())
				if def := p.PropertySchema.Default; def != nil && typ == "integer" {
					n, err := strconv.Atoi(fmt.Sprint(*def))
					if err != nil {
						return fmt.Errorf("%s: default %v is not an integer", p.Field.Name, *def)
					}
					p.PropertySchema.WithDefault(n)
				}
			}
			// The reflector leaves "type: object" beside a nullable
			// reference, which would forbid the null it allows.
			if len(p.PropertySchema.AnyOf) > 0 {
				p.PropertySchema.Type = nil
				for _, option := range p.PropertySchema.AnyOf {
					if option.TypeObject != nil && option.TypeObject.Ref != nil {
						option.TypeObject.Type = nil
					}
				}
			}
			return nil
		}),
	)

	ids := map[string]string{}
	for _, route := range routes {
		where := route.Method + " " + route.Path
		if route.Doc.ID == "" || route.Doc.Summary == "" {
			errs = append(errs, fmt.Errorf("%s: Doc needs an ID and a Summary", where))
		}
		if other, dup := ids[route.Doc.ID]; dup {
			errs = append(errs, fmt.Errorf("%s: operation ID %q is also used by %s", where, route.Doc.ID, other))
		}
		ids[route.Doc.ID] = where
		if err := addOperation(r, route); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", where, err))
		}
	}
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	return r.Spec, nil
}

// Generate returns the OpenAPI document for routes as YAML.
func Generate(routes []api.Route) ([]byte, error) {
	spec, err := Build(routes)
	if err != nil {
		return nil, err
	}
	return spec.MarshalYAML()
}

// addOperation adds one route to the document.
func addOperation(r *openapi31.Reflector, route api.Route) error {
	oc, err := r.NewOperationContext(route.Method, route.Path)
	if err != nil {
		return err
	}
	d := route.Doc
	oc.SetID(d.ID)
	oc.SetSummary(d.Summary)
	if d.Description != "" {
		oc.SetDescription(d.Description)
	}
	oc.SetTags(tag(route.Path))

	switch route.Auth {
	case api.AuthSession:
		oc.AddSecurity(sessionCookie)
		oc.AddSecurity(sessionBearer)
	case api.AuthOpsKeyOrSession:
		oc.AddSecurity(opsKey)
		oc.AddSecurity(sessionCookie)
		oc.AddSecurity(sessionBearer)
	case api.AuthOpsKey:
		oc.AddSecurity(opsKey)
	case api.AuthMetricsKey:
		oc.AddSecurity(metricsKey)
	}

	oc.AddReqStructure(pathParams(route.Path))
	if headers := writeHeaders(route); headers != nil {
		oc.AddReqStructure(headers)
	}
	if d.Query != nil {
		oc.AddReqStructure(d.Query)
	}
	if d.Body != nil {
		oc.AddReqStructure(d.Body, openapi.WithContentType("application/json"))
	}

	status := d.Status
	if status == 0 {
		status = http.StatusOK
	}
	switch {
	case d.ContentType != "":
		oc.AddRespStructure(nil, openapi.WithHTTPStatus(status), openapi.WithContentType(d.ContentType))
	default:
		oc.AddRespStructure(d.Response, openapi.WithHTTPStatus(status))
	}
	for _, also := range d.Also {
		oc.AddRespStructure(also.Body, openapi.WithHTTPStatus(also.Status), func(cu *openapi.ContentUnit) {
			cu.Description = also.Description
		})
	}
	for _, code := range route.ErrorStatuses() {
		oc.AddRespStructure(api.ErrorBody(), openapi.WithHTTPStatus(code))
	}
	oc.AddRespStructure(api.ErrorBody(), func(cu *openapi.ContentUnit) {
		cu.IsDefault = true
		cu.Description = "Any other error, such as a 500"
	})
	if err := r.AddOperation(oc); err != nil {
		return err
	}
	if d.Body != nil {
		return nil
	}
	// The parameter structs leave an empty object body on writes that take
	// none.
	return r.Spec.SetupOperation(route.Method, route.Path, func(op *openapi31.Operation) error {
		op.RequestBody = nil
		return nil
	})
}

// tag groups an operation by the first path segment that names a resource:
// the module for module routes, and otherwise the guild or top-level
// resource.
func tag(path string) string {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	switch {
	case len(parts) >= 4 && parts[0] == "guilds" && parts[2] == "modules":
		return parts[3]
	case len(parts) >= 3 && parts[0] == "guilds":
		return parts[2]
	case parts[0] == "guilds", parts[0] == "auth", parts[0] == "members":
		return parts[0]
	default:
		return "health"
	}
}

// pathParams returns a struct value declaring path's {placeholders} as
// string path parameters.
func pathParams(path string) any {
	var fields []reflect.StructField
	for _, segment := range strings.Split(path, "/") {
		name, ok := strings.CutPrefix(segment, "{")
		if !ok {
			continue
		}
		name = strings.TrimSuffix(name, "}")
		fields = append(fields, reflect.StructField{
			Name: "P" + strconv.Itoa(len(fields)),
			Type: reflect.TypeFor[string](),
			Tag:  reflect.StructTag(`json:"-" path:"` + name + `"`),
		})
	}
	return reflect.New(reflect.StructOf(fields)).Elem().Interface()
}

// writeHeaders returns a struct value declaring the headers a write takes,
// or nil for a read.
func writeHeaders(route api.Route) any {
	var fields []reflect.StructField
	if route.Idempotent {
		fields = append(fields, reflect.StructField{
			Name: "IdempotencyKey",
			Type: reflect.TypeFor[string](),
			Tag: `json:"-" header:"Idempotency-Key" required:"true" maxLength:"256" ` +
				`description:"Makes the write safe to retry; reuse the key only for the identical request."`,
		})
	}
	if route.Auth == api.AuthSession && route.Method != http.MethodGet {
		fields = append(fields, reflect.StructField{
			Name: "CSRFToken",
			Type: reflect.TypeFor[string](),
			Tag:  `json:"-" header:"X-CSRF-Token" description:"The CSRF cookie's value. Required with the session cookie; not with a bearer token."`,
		})
	}
	if fields == nil {
		return nil
	}
	return reflect.New(reflect.StructOf(fields)).Elem().Interface()
}

// collectStructs adds every struct type reachable from t to seen.
func collectStructs(t reflect.Type, seen map[reflect.Type]bool) {
	if t == nil {
		return
	}
	switch t.Kind() {
	case reflect.Pointer, reflect.Slice, reflect.Array, reflect.Map:
		collectStructs(t.Elem(), seen)
	case reflect.Struct:
		if seen[t] {
			return
		}
		seen[t] = true
		for field := range t.Fields() {
			if field.IsExported() || field.Anonymous {
				collectStructs(field.Type, seen)
			}
		}
	}
}

// alwaysPresent returns the JSON names of t's fields that encoding/json
// always writes: exported, not "-", and not omitempty or omitzero. Embedded
// structs without a JSON name contribute their own fields, as encoding/json
// flattens them.
func alwaysPresent(t reflect.Type) []string {
	var names []string
	for field := range t.Fields() {
		tag, hasTag := field.Tag.Lookup("json")
		name, options, _ := strings.Cut(tag, ",")
		if tag == "-" {
			continue
		}
		if field.Anonymous && name == "" {
			embedded := field.Type
			if embedded.Kind() == reflect.Pointer {
				embedded = embedded.Elem()
			}
			if embedded.Kind() == reflect.Struct {
				names = append(names, alwaysPresent(embedded)...)
				continue
			}
		}
		if !field.IsExported() {
			continue
		}
		if !hasTag || name == "" {
			name = field.Name
		}
		if slices.ContainsFunc(strings.Split(options, ","), func(o string) bool {
			return o == "omitempty" || o == "omitzero"
		}) {
			continue
		}
		names = append(names, name)
	}
	return names
}

// enumScanner finds the values of string enum types in this module by
// reading their typed constants from source, so a new constant reaches the
// contract without anyone listing it.
type enumScanner struct {
	root   string
	values map[reflect.Type][]any
}

// enum returns the typed string constants declared for t in its package, in
// source order. A type with no such constants is an open string, and gets no
// enum.
func (s enumScanner) enum(t reflect.Type) ([]any, error) {
	if values, ok := s.values[t]; ok {
		return values, nil
	}
	rel, ok := strings.CutPrefix(t.PkgPath(), modulePath+"/")
	if !ok {
		return nil, nil
	}
	fset := token.NewFileSet()
	dir := filepath.Join(s.root, filepath.FromSlash(rel))
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read %s for %s values: %w", dir, t, err)
	}
	var values []any
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, filepath.Join(dir, name), nil, 0)
		if err != nil {
			return nil, err
		}
		for _, decl := range file.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok || gen.Tok != token.CONST {
				continue
			}
			for _, spec := range gen.Specs {
				values = append(values, typedStrings(spec.(*ast.ValueSpec), t.Name())...)
			}
		}
	}
	s.values[t] = values
	return values, nil
}

// typedStrings returns the string literal values spec declares with type
// name typeName.
func typedStrings(spec *ast.ValueSpec, typeName string) []any {
	ident, ok := spec.Type.(*ast.Ident)
	if !ok || ident.Name != typeName {
		return nil
	}
	var values []any
	for _, value := range spec.Values {
		lit, ok := value.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			continue
		}
		if s, err := strconv.Unquote(lit.Value); err == nil {
			values = append(values, s)
		}
	}
	return values
}

// findModuleRoot returns the directory of the go.mod above the working
// directory: apps/backend when run by go generate or go test.
func findModuleRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("contract: no go.mod above the working directory")
		}
		dir = parent
	}
}
