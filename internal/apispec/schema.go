package apispec

import (
	"encoding"
	"encoding/json"
	"fmt"
	"maps"
	"reflect"
	"strings"
	"time"
	"unicode"
)

// Schema is a JSON Schema (OpenAPI 3.1) object. A plain map keeps the
// generated document's key order deterministic (encoding/json sorts keys).
type Schema = map[string]any

// Schemaer lets a type that customises its JSON encoding describe itself.
// Types implementing json.Marshaler or encoding.TextMarshaler must implement
// it; reflecting their Go structure would describe the wrong wire format.
type Schemaer interface {
	OpenAPISchema() Schema
}

var (
	timeType        = reflect.TypeFor[time.Time]()
	rawMessageType  = reflect.TypeFor[json.RawMessage]()
	schemaerType    = reflect.TypeFor[Schemaer]()
	marshalerType   = reflect.TypeFor[json.Marshaler]()
	textMarshalType = reflect.TypeFor[encoding.TextMarshaler]()
)

// generator turns Go types into schemas, collecting named structs into
// components.schemas.
type generator struct {
	components map[string]Schema
	names      map[reflect.Type]string
	owners     map[string]reflect.Type
	overrides  map[reflect.Type]string
}

func newGenerator(overrides map[reflect.Type]string) *generator {
	return &generator{
		components: map[string]Schema{},
		names:      map[reflect.Type]string{},
		owners:     map[string]reflect.Type{},
		overrides:  overrides,
	}
}

func implements(t, iface reflect.Type) bool {
	return t.Implements(iface) || reflect.PointerTo(t).Implements(iface)
}

// customSchema returns the Schemaer schema of t, if any.
func customSchema(t reflect.Type) (Schema, bool) {
	var v reflect.Value
	switch {
	case t.Implements(schemaerType):
		v = reflect.Zero(t)
	case reflect.PointerTo(t).Implements(schemaerType):
		v = reflect.New(t)
	default:
		return nil, false
	}
	return v.Interface().(Schemaer).OpenAPISchema(), true
}

// schema returns the schema for t. Values of t may encode as JSON null when
// t is a pointer, slice, map or interface; callers decide whether that is
// reachable (omitempty fields never encode a nil value).
func (g *generator) schema(t reflect.Type) (Schema, error) {
	// Unwrap pointers first: encoding/json encodes nil as null and otherwise
	// the element (whose Marshaler, if any, is then what matters).
	if t.Kind() == reflect.Pointer {
		inner, err := g.schema(t.Elem())
		if err != nil {
			return nil, err
		}
		return nullable(inner), nil
	}
	if s, ok := customSchema(t); ok {
		return s, nil
	}
	switch t {
	case timeType:
		return Schema{"type": "string", "format": "date-time"}, nil
	case rawMessageType:
		return Schema{}, nil
	}
	if implements(t, marshalerType) || implements(t, textMarshalType) {
		return nil, fmt.Errorf("%s customises its JSON encoding; implement apispec.Schemaer", t)
	}

	switch t.Kind() {
	case reflect.Bool:
		return Schema{"type": "boolean"}, nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return Schema{"type": "integer"}, nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return Schema{"type": "integer", "minimum": 0}, nil
	case reflect.Float32, reflect.Float64:
		return Schema{"type": "number"}, nil
	case reflect.String:
		return Schema{"type": "string"}, nil
	case reflect.Interface:
		return Schema{}, nil
	case reflect.Slice:
		if t.Elem().Kind() == reflect.Uint8 && !implements(t.Elem(), marshalerType) {
			return nullable(Schema{"type": "string", "contentEncoding": "base64"}), nil
		}
		items, err := g.schema(t.Elem())
		if err != nil {
			return nil, err
		}
		return nullable(Schema{"type": "array", "items": items}), nil
	case reflect.Array:
		items, err := g.schema(t.Elem())
		if err != nil {
			return nil, err
		}
		return Schema{"type": "array", "items": items, "minItems": t.Len(), "maxItems": t.Len()}, nil
	case reflect.Map:
		switch k := t.Key(); {
		case k.Kind() == reflect.String, k.Kind() >= reflect.Int && k.Kind() <= reflect.Uintptr,
			implements(k, textMarshalType):
		default:
			return nil, fmt.Errorf("%s: unsupported map key type %s", t, k)
		}
		vals, err := g.schema(t.Elem())
		if err != nil {
			return nil, err
		}
		return nullable(Schema{"type": "object", "additionalProperties": vals}), nil
	case reflect.Struct:
		if t.Name() == "" {
			return g.object(t)
		}
		return g.ref(t)
	}
	return nil, fmt.Errorf("unsupported type %s", t)
}

// ref registers the named struct t in components and returns a $ref to it.
func (g *generator) ref(t reflect.Type) (Schema, error) {
	if name, ok := g.names[t]; ok {
		return Schema{"$ref": "#/components/schemas/" + name}, nil
	}
	name := g.overrides[t]
	if name == "" {
		r := []rune(t.Name())
		r[0] = unicode.ToUpper(r[0])
		name = string(r)
	}
	if other, ok := g.owners[name]; ok && other != t {
		return nil, fmt.Errorf("schema name %q used by both %s and %s; set Registry.Names for one", name, other, t)
	}
	g.names[t] = name
	g.owners[name] = t
	obj, err := g.object(t) // registered first, so recursive types terminate
	if err != nil {
		return nil, err
	}
	g.components[name] = obj
	return Schema{"$ref": "#/components/schemas/" + name}, nil
}

// field is one JSON-visible struct field after encoding/json's rules.
type field struct {
	name      string
	typ       reflect.Type
	omitEmpty bool // omitempty/omitzero, or promoted through a nil-able embedded pointer
	asString  bool
	tagged    bool
	depth     int
	tag       reflect.StructTag
}

// jsonFields lists the fields encoding/json would emit for t, resolving
// embedded structs the way encoding/json does: the shallowest field wins,
// a tagged one beats an untagged one at equal depth, and remaining ties
// are dropped.
func jsonFields(t reflect.Type) []field {
	var all []field
	var walk func(t reflect.Type, depth int, optional bool, seen map[reflect.Type]bool)
	walk = func(t reflect.Type, depth int, optional bool, seen map[reflect.Type]bool) {
		if seen[t] {
			return
		}
		seen[t] = true
		defer delete(seen, t)
		for sf := range t.Fields() {
			tag := sf.Tag.Get("json")
			if tag == "-" {
				continue
			}
			name, opts, _ := strings.Cut(tag, ",")
			ft := sf.Type
			if sf.Anonymous {
				et := ft
				if et.Kind() == reflect.Pointer {
					et = et.Elem()
				}
				if name == "" && et.Kind() == reflect.Struct {
					// encoding/json omits all fields of a nil embedded pointer.
					walk(et, depth+1, optional || ft.Kind() == reflect.Pointer, seen)
					continue
				}
				if !sf.IsExported() {
					continue
				}
			} else if !sf.IsExported() {
				continue
			}
			f := field{name: name, typ: ft, depth: depth, tag: sf.Tag, tagged: name != "", omitEmpty: optional}
			if f.name == "" {
				f.name = sf.Name
			}
			for o := range strings.SplitSeq(opts, ",") {
				switch o {
				case "omitempty", "omitzero":
					f.omitEmpty = true
				case "string":
					f.asString = true
				}
			}
			all = append(all, f)
		}
	}
	walk(t, 0, false, map[reflect.Type]bool{})

	byName := map[string][]field{}
	var order []string
	for _, f := range all {
		if _, ok := byName[f.name]; !ok {
			order = append(order, f.name)
		}
		byName[f.name] = append(byName[f.name], f)
	}
	var out []field
	for _, name := range order {
		fs := byName[name]
		best := fs[0]
		ties := 0
		for _, f := range fs[1:] {
			switch {
			case f.depth < best.depth, f.depth == best.depth && f.tagged && !best.tagged:
				best, ties = f, 0
			case f.depth == best.depth && f.tagged == best.tagged:
				ties++
			}
		}
		if ties == 0 {
			out = append(out, best)
		}
	}
	return out
}

// object describes struct t inline.
func (g *generator) object(t reflect.Type) (Schema, error) {
	props := Schema{}
	var required []string
	for _, f := range jsonFields(t) {
		var s Schema
		var err error
		if f.asString {
			s = Schema{"type": "string"}
		} else {
			s, err = g.schema(f.typ)
			if err != nil {
				return nil, fmt.Errorf("%s.%s: %w", t, f.name, err)
			}
		}
		if f.omitEmpty {
			// An omitted nil never appears on the wire, so a present value is
			// never null.
			s = nonNull(s)
		}
		s = annotate(s, f.tag)
		props[f.name] = s
		if !f.omitEmpty {
			required = append(required, f.name)
		}
	}
	obj := Schema{"type": "object", "properties": props}
	if len(required) > 0 {
		obj["required"] = required
	}
	return obj, nil
}

// annotate adds doc:"…" and enum:"a,b" tag metadata. A $ref cannot carry
// siblings portably, so it is wrapped in allOf when annotated.
func annotate(s Schema, tag reflect.StructTag) Schema {
	doc, enum := tag.Get("doc"), tag.Get("enum")
	if doc == "" && enum == "" {
		return s
	}
	if _, isRef := s["$ref"]; isRef {
		s = Schema{"allOf": []any{s}}
	}
	out := maps.Clone(s)
	if doc != "" {
		out["description"] = doc
	}
	if enum != "" {
		out["enum"] = strings.Split(enum, ",")
	}
	return out
}

// nullable allows JSON null in addition to s.
func nullable(s Schema) Schema {
	if _, isRef := s["$ref"]; isRef {
		return Schema{"anyOf": []any{s, Schema{"type": "null"}}}
	}
	typ, ok := s["type"].(string)
	if !ok {
		return s // {} already admits null
	}
	out := maps.Clone(s)
	out["type"] = []any{typ, "null"}
	return out
}

// nonNull undoes nullable.
func nonNull(s Schema) Schema {
	if alts, ok := s["anyOf"].([]any); ok && len(alts) == 2 && len(s) == 1 {
		if n, ok := alts[1].(Schema); ok && n["type"] == "null" {
			return alts[0].(Schema)
		}
	}
	if typ, ok := s["type"].([]any); ok && len(typ) == 2 && typ[1] == "null" {
		out := maps.Clone(s)
		out["type"] = typ[0]
		return out
	}
	return s
}
