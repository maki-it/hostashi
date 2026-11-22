// SPDX-License-Identifier: AGPL-3.0-or-later

// Package apicli turns the embedded OpenAPI document into CLI commands
// (ADR-0058): every operation is "hostashi api TAG VERB", so the CLI can
// do what the API can without a hand-written command per route.
package apicli

import (
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strings"
	"unicode"
)

// Param is a path or query parameter.
type Param struct {
	Name        string
	Type        string // string, integer, boolean, number or array
	Required    bool
	Description string
}

// Field is a property of a JSON or multipart request body.
type Field struct {
	Name string
	Type string // as Param.Type, plus object, file or "" (any JSON)
}

// Op is one API operation as a command.
type Op struct {
	Tag, Verb, ID string
	Method, Path  string
	Summary       string
	PathParams    []Param
	Query         []Param
	// BodyType is the request's content type, "" without a body.
	BodyType     string
	BodyRequired bool
	Fields       []Field
	// Scope is the token scope the route needs; SessionOnly routes refuse
	// API tokens.
	Scope       string
	SessionOnly bool
	Public      bool
}

// Spec is the parsed document.
type Spec struct {
	Ops []*Op
}

type document struct {
	Paths      map[string]map[string]operation `json:"paths"`
	Components struct {
		Schemas map[string]schema `json:"schemas"`
	} `json:"components"`
}

type operation struct {
	OperationID string                `json:"operationId"`
	Summary     string                `json:"summary"`
	Tags        []string              `json:"tags"`
	Parameters  []parameter           `json:"parameters"`
	RequestBody *requestBody          `json:"requestBody"`
	Security    []map[string][]string `json:"security"`
}

type parameter struct {
	Name        string `json:"name"`
	In          string `json:"in"`
	Required    bool   `json:"required"`
	Description string `json:"description"`
	Schema      schema `json:"schema"`
}

type requestBody struct {
	Required bool                 `json:"required"`
	Content  map[string]mediaType `json:"content"`
}

type mediaType struct {
	Schema schema `json:"schema"`
}

type schema struct {
	Ref        string            `json:"$ref"`
	Type       json.RawMessage   `json:"type"`
	Format     string            `json:"format"`
	Properties map[string]schema `json:"properties"`
}

// typeOf is the schema's type without "null".
func (s schema) typeOf() string {
	var one string
	if json.Unmarshal(s.Type, &one) == nil {
		return one
	}

	var many []string
	if json.Unmarshal(s.Type, &many) == nil {
		for _, t := range many {
			if t != "null" {
				return t
			}
		}
	}

	return ""
}

var methods = []string{"get", "post", "put", "patch", "delete"}

// Load parses the OpenAPI document.
func Load(raw []byte) (*Spec, error) {
	var d document
	if err := json.Unmarshal(raw, &d); err != nil {
		return nil, fmt.Errorf("apicli: %w", err)
	}

	resolve := func(s schema) schema {
		if name, ok := strings.CutPrefix(s.Ref, "#/components/schemas/"); ok {
			return d.Components.Schemas[name]
		}

		return s
	}

	sp := &Spec{}

	for path, ops := range d.Paths {
		for _, m := range methods {
			o, ok := ops[m]
			if !ok {
				continue
			}

			op := &Op{ID: o.OperationID, Method: strings.ToUpper(m), Path: path, Summary: o.Summary}
			if len(o.Tags) > 0 {
				op.Tag = o.Tags[0]
			}

			for _, p := range o.Parameters {
				q := Param{Name: p.Name, Type: resolve(p.Schema).typeOf(), Required: p.Required, Description: p.Description}

				switch p.In {
				case "path":
					op.PathParams = append(op.PathParams, q)
				case "query":
					op.Query = append(op.Query, q)
				}
			}

			// Path parameters in the order of the path.
			sort.SliceStable(op.PathParams, func(i, j int) bool {
				return strings.Index(path, "{"+op.PathParams[i].Name) < strings.Index(path, "{"+op.PathParams[j].Name)
			})

			if rb := o.RequestBody; rb != nil {
				op.BodyRequired = rb.Required

				for _, ct := range []string{"application/json", "multipart/form-data", "application/octet-stream"} {
					mt, ok := rb.Content[ct]
					if !ok {
						continue
					}

					op.BodyType = ct

					for name, ps := range resolve(mt.Schema).Properties {
						t := resolve(ps).typeOf()
						if ps.Format == "binary" {
							t = "file"
						}

						op.Fields = append(op.Fields, Field{Name: name, Type: t})
					}

					slices.SortFunc(op.Fields, func(a, b Field) int { return strings.Compare(a.Name, b.Name) })

					break
				}
			}

			op.Public = len(o.Security) == 0

			for _, sec := range o.Security {
				if scopes, ok := sec["token"]; ok && len(scopes) > 0 {
					op.Scope = scopes[0]
				}
			}

			op.SessionOnly = !op.Public && op.Scope == ""

			sp.Ops = append(sp.Ops, op)
		}
	}

	slices.SortFunc(sp.Ops, func(a, b *Op) int {
		if c := strings.Compare(a.Tag, b.Tag); c != 0 {
			return c
		}

		return strings.Compare(a.ID, b.ID)
	})

	sp.nameVerbs()

	return sp, nil
}

// nameVerbs gives each operation a verb unique within its tag: the
// operation id in kebab case without the tag's noun, e.g. getSite in
// "sites" is "get" and setSiteLimits is "set-limits". Where that clashes,
// the whole id is used.
func (sp *Spec) nameVerbs() {
	seen := map[string]int{}

	for _, op := range sp.Ops {
		op.Verb = shortVerb(op.ID, op.Tag)
		seen[op.Tag+" "+op.Verb]++
	}

	for _, op := range sp.Ops {
		if seen[op.Tag+" "+op.Verb] > 1 {
			op.Verb = Kebab(op.ID)
		}
	}
}

// extraNouns are words a tag's ids repeat that are not the tag's name.
var extraNouns = map[string][]string{"migrations": {"cloud", "panel"}}

func shortVerb(id, tag string) string {
	words := strings.Split(Kebab(id), "-")
	noun := strings.Split(strings.ReplaceAll(tag, "_", "-"), "-")

	if extra, ok := extraNouns[tag]; ok {
		if i := indexRun(words, extra); i > 0 {
			words = slices.Concat(words[:i], words[i+len(extra):])
		}
	}

	// Singular and plural of the tag's noun phrase.
	single := slices.Clone(noun)
	single[len(single)-1] = strings.TrimSuffix(single[len(single)-1], "s")

	for _, n := range [][]string{noun, single} {
		if i := indexRun(words, n); i > 0 {
			return strings.Join(slices.Concat(words[:i], words[i+len(n):]), "-")
		}
	}

	return strings.Join(words, "-")
}

// indexRun finds run in words after the first word (the verb); -1 if not.
func indexRun(words, run []string) int {
	for i := 1; i+len(run) <= len(words); i++ {
		if slices.Equal(words[i:i+len(run)], run) {
			return i
		}
	}

	return -1
}

// Kebab turns an operation id into kebab case: getPHPSettings is
// get-php-settings.
func Kebab(id string) string {
	var b strings.Builder

	rs := []rune(strings.ReplaceAll(id, "PostgreSQL", "Postgresql"))
	for i, r := range rs {
		if unicode.IsUpper(r) && i > 0 {
			prevLower := unicode.IsLower(rs[i-1]) || unicode.IsDigit(rs[i-1])
			nextLower := i+1 < len(rs) && unicode.IsLower(rs[i+1])

			if prevLower || (unicode.IsUpper(rs[i-1]) && nextLower) {
				_ = b.WriteByte('-')
			}
		}

		_, _ = b.WriteRune(unicode.ToLower(r))
	}

	return b.String()
}

// Tags lists the tags in order.
func (sp *Spec) Tags() []string {
	var out []string

	for _, op := range sp.Ops {
		if len(out) == 0 || out[len(out)-1] != op.Tag {
			out = append(out, op.Tag)
		}
	}

	return out
}

// InTag lists the operations of tag.
func (sp *Spec) InTag(tag string) []*Op {
	var out []*Op

	for _, op := range sp.Ops {
		if op.Tag == tag {
			out = append(out, op)
		}
	}

	return out
}

// Find returns the operation tag verb, also by its operation id.
func (sp *Spec) Find(tag, verb string) *Op {
	for _, op := range sp.Ops {
		if op.Tag == tag && (op.Verb == verb || op.ID == verb || Kebab(op.ID) == verb) {
			return op
		}
	}

	return nil
}
