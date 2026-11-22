// SPDX-License-Identifier: AGPL-3.0-or-later

package apicli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/url"
	"os"
	"strconv"
	"strings"
)

// Request is an HTTP request built from command arguments.
type Request struct {
	Method      string
	Path        string // below /api/v1, with the query
	ContentType string
	Body        io.Reader
}

// Input is where a request reads from: stdin for "-d -" and files for
// "@file" values.
type Input struct {
	Stdin    io.Reader
	ReadFile func(string) ([]byte, error)
	Open     func(string) (io.ReadCloser, error)
}

// OSInput reads from the real stdin and files.
func OSInput() Input {
	return Input{
		Stdin:    os.Stdin,
		ReadFile: os.ReadFile,
		Open:     func(p string) (io.ReadCloser, error) { return os.Open(p) },
	}
}

// Build turns arguments into a request. The path parameters come first,
// in order; then key=value (a string, or the field's type), key:=JSON
// (any JSON value) and key=@file (a file part of a multipart form), and
// -d JSON, -d @file or -d - for the whole body.
func (op *Op) Build(args []string, in Input) (Request, error) {
	if len(args) < len(op.PathParams) {
		return Request{}, fmt.Errorf("missing %s", op.Usage())
	}

	path := op.Path
	for i, v := range args[:len(op.PathParams)] {
		p := op.PathParams[i]
		if v == "" {
			return Request{}, fmt.Errorf("%s must not be empty", p.Name)
		}

		path = strings.Replace(path, "{"+p.Name+"}", url.PathEscape(v), 1)
	}

	path = strings.TrimPrefix(path, "/api/v1")

	var (
		raw      []byte
		rawSet   bool
		fields   = map[string]any{}
		files    = map[string]string{}
		query    = url.Values{}
		rest     = args[len(op.PathParams):]
		isQuery  = map[string]Param{}
		fieldsOf = map[string]Field{}
	)

	for _, q := range op.Query {
		isQuery[q.Name] = q
	}

	for _, f := range op.Fields {
		fieldsOf[f.Name] = f
	}

	for len(rest) > 0 {
		a := rest[0]
		rest = rest[1:]

		if a == "-d" || a == "--data" {
			if len(rest) == 0 {
				return Request{}, errors.New("-d needs a value: JSON, @file or - for stdin")
			}

			v := rest[0]
			rest = rest[1:]

			b, err := readData(v, in)
			if err != nil {
				return Request{}, err
			}

			raw, rawSet = b, true

			continue
		}

		key, val, isJSON, ok := splitArg(a)
		if !ok {
			return Request{}, fmt.Errorf("argument %q: want key=value, key:=JSON or -d", a)
		}

		if _, ok := isQuery[key]; ok {
			query.Add(key, val)

			continue
		}

		if op.BodyType == "" {
			return Request{}, fmt.Errorf("unknown parameter %q; %s takes %s", key, op.Verb, op.paramNames())
		}

		f, known := fieldsOf[key]
		if !known && (op.BodyType != "application/json" || len(op.Fields) > 0) {
			return Request{}, fmt.Errorf("unknown field %q; %s takes %s", key, op.Verb, op.paramNames())
		}

		if f.Type == "file" {
			p, ok := strings.CutPrefix(val, "@")
			if !ok {
				return Request{}, fmt.Errorf("%s is a file: write %s=@PATH", key, key)
			}

			files[key] = p

			continue
		}

		v, err := fieldValue(f, val, isJSON)
		if err != nil {
			return Request{}, fmt.Errorf("%s: %w", key, err)
		}

		fields[key] = v
	}

	for _, q := range op.Query {
		if q.Required && !query.Has(q.Name) {
			return Request{}, fmt.Errorf("%s=… is required", q.Name)
		}
	}

	r := Request{Method: op.Method, Path: path}
	if len(query) > 0 {
		r.Path += "?" + query.Encode()
	}

	switch op.BodyType {
	case "":
		if rawSet {
			return Request{}, fmt.Errorf("%s takes no body", op.Verb)
		}
	case "application/json":
		r.ContentType = op.BodyType

		switch {
		case rawSet && len(fields) > 0:
			return Request{}, errors.New("use either -d or key=value fields, not both")
		case rawSet:
			if !json.Valid(raw) {
				return Request{}, errors.New("-d: not valid JSON")
			}

			r.Body = bytes.NewReader(raw)
		case len(fields) > 0 || op.BodyRequired:
			b, err := json.Marshal(fields)
			if err != nil {
				return Request{}, err
			}

			r.Body = bytes.NewReader(b)
		default:
			r.ContentType = ""
		}
	case "application/octet-stream":
		if !rawSet {
			return Request{}, fmt.Errorf("%s sends a file: add -d @PATH or -d - for stdin", op.Verb)
		}

		r.ContentType, r.Body = op.BodyType, bytes.NewReader(raw)
	case "multipart/form-data":
		body, ct, err := multipartBody(fields, files, in)
		if err != nil {
			return Request{}, err
		}

		r.ContentType, r.Body = ct, body
	}

	return r, nil
}

func splitArg(a string) (key, val string, isJSON, ok bool) {
	i := strings.IndexByte(a, '=')
	if i <= 0 {
		return "", "", false, false
	}

	key, val = a[:i], a[i+1:]
	if k, isJ := strings.CutSuffix(key, ":"); isJ {
		return k, val, true, k != ""
	}

	return key, val, false, true
}

func readData(v string, in Input) ([]byte, error) {
	switch {
	case v == "-":
		return io.ReadAll(in.Stdin)
	case strings.HasPrefix(v, "@"):
		return in.ReadFile(v[1:])
	default:
		return []byte(v), nil
	}
}

// fieldValue converts a command line value to the field's JSON type.
func fieldValue(f Field, val string, isJSON bool) (any, error) {
	if isJSON {
		var v any
		if err := json.Unmarshal([]byte(val), &v); err != nil {
			return nil, fmt.Errorf("not valid JSON: %w", err)
		}

		return v, nil
	}

	switch f.Type {
	case "integer":
		n, err := strconv.ParseInt(val, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("%q is not a whole number", val)
		}

		return n, nil
	case "number":
		n, err := strconv.ParseFloat(val, 64)
		if err != nil {
			return nil, fmt.Errorf("%q is not a number", val)
		}

		return n, nil
	case "boolean":
		b, err := strconv.ParseBool(val)
		if err != nil {
			return nil, fmt.Errorf("%q is not true or false", val)
		}

		return b, nil
	case "array":
		// a,b,c for a list of strings; key:='[…]' for anything else.
		if val == "" {
			return []string{}, nil
		}

		return strings.Split(val, ","), nil
	case "object":
		return nil, errors.New("an object: write key:='{…}'")
	default:
		return val, nil
	}
}

func multipartBody(fields map[string]any, files map[string]string, in Input) (io.Reader, string, error) {
	var buf bytes.Buffer

	w := multipart.NewWriter(&buf)

	for k, v := range fields {
		if err := w.WriteField(k, fmt.Sprint(v)); err != nil {
			return nil, "", err
		}
	}

	for k, p := range files {
		f, err := in.Open(p)
		if err != nil {
			return nil, "", err
		}

		part, err := w.CreateFormFile(k, baseName(p))
		if err == nil {
			_, err = io.Copy(part, f)
		}

		_ = f.Close()

		if err != nil {
			return nil, "", err
		}
	}

	if err := w.Close(); err != nil {
		return nil, "", err
	}

	return &buf, w.FormDataContentType(), nil
}

func baseName(p string) string {
	if i := strings.LastIndexAny(p, `/\`); i >= 0 {
		return p[i+1:]
	}

	return p
}

// Usage is the argument line of the command, e.g. "NAME [key=value …]".
func (op *Op) Usage() string {
	parts := []string{op.Tag, op.Verb}

	for _, p := range op.PathParams {
		parts = append(parts, strings.ToUpper(p.Name))
	}

	for _, q := range op.Query {
		if q.Required {
			parts = append(parts, q.Name+"=…")
		}
	}

	switch op.BodyType {
	case "application/json":
		if len(op.Fields) > 0 {
			parts = append(parts, "[field=value …]")
		} else {
			parts = append(parts, "[-d JSON]")
		}
	case "application/octet-stream":
		parts = append(parts, "-d @FILE")
	case "multipart/form-data":
		parts = append(parts, "field=value … file=@FILE")
	}

	if len(op.Query) > 0 {
		parts = append(parts, "[param=value …]")
	}

	return strings.Join(parts, " ")
}

func (op *Op) paramNames() string {
	var names []string

	for _, q := range op.Query {
		names = append(names, q.Name)
	}

	for _, f := range op.Fields {
		names = append(names, f.Name)
	}

	if len(names) == 0 {
		return "no parameters"
	}

	return strings.Join(names, ", ")
}
