package recommend

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"sort"
	"strings"
	"unicode/utf8"
)

const maxInputBytes = 1 << 20
const maxEntries = 4096

var errInput = errors.New("invalid input")

// decode first checks duplicate keys and exact schema spelling. encoding/json's
// case-insensitive struct matching and last-key-wins behavior are not admitted.
// Errors deliberately contain no caller bytes or filesystem paths.
func decode(data []byte, out any, path string) ([]string, error) {
	if len(bytes.TrimSpace(data)) == 0 {
		return []string{path}, nil
	}
	if len(data) > maxInputBytes || !utf8.Valid(data) {
		return nil, errInput
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	if err := uniqueValue(d, 0); err != nil {
		return nil, errInput
	}
	if _, err := d.Token(); err != io.EOF {
		return nil, errInput
	}
	var missing []string
	if err := shape(data, reflect.TypeOf(out).Elem(), path, &missing); err != nil {
		return nil, errInput
	}
	if err := json.Unmarshal(data, out); err != nil {
		return nil, errInput
	}
	sort.Strings(missing)
	return missing, nil
}

func uniqueValue(d *json.Decoder, depth int) error {
	if depth > 32 {
		return errInput
	}
	token, err := d.Token()
	if err != nil {
		return err
	}
	if s, ok := token.(string); ok && strings.ContainsRune(s, utf8.RuneError) {
		return errInput
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		seen := map[string]bool{}
		for d.More() {
			k, err := d.Token()
			if err != nil {
				return err
			}
			key, ok := k.(string)
			if !ok || seen[key] || strings.ContainsRune(key, utf8.RuneError) {
				return errInput
			}
			seen[key] = true
			if err := uniqueValue(d, depth+1); err != nil {
				return err
			}
		}
		end, err := d.Token()
		if err != nil || end != json.Delim('}') {
			return errInput
		}
	case '[':
		for n := 0; d.More(); n++ {
			if n >= maxEntries {
				return errInput
			}
			if err := uniqueValue(d, depth+1); err != nil {
				return err
			}
		}
		end, err := d.Token()
		if err != nil || end != json.Delim(']') {
			return errInput
		}
	default:
		return errInput
	}
	return nil
}

func shape(data []byte, typ reflect.Type, path string, missing *[]string) error {
	data = bytes.TrimSpace(data)
	if bytes.Equal(data, []byte("null")) {
		return errInput
	}
	switch typ.Kind() {
	case reflect.Struct:
		var object map[string]json.RawMessage
		if err := json.Unmarshal(data, &object); err != nil || object == nil {
			return errInput
		}
		fields := map[string]reflect.Type{}
		for i := 0; i < typ.NumField(); i++ {
			f := typ.Field(i)
			fields[f.Tag.Get("json")] = f.Type
		}
		for name := range object {
			if _, ok := fields[name]; !ok {
				return errInput
			}
		}
		for i := 0; i < typ.NumField(); i++ {
			f := typ.Field(i)
			name := f.Tag.Get("json")
			v, ok := object[name]
			if !ok {
				*missing = append(*missing, path+"."+name)
				continue
			}
			if err := shape(v, f.Type, path+"."+name, missing); err != nil {
				return err
			}
		}
	case reflect.Slice:
		var values []json.RawMessage
		if err := json.Unmarshal(data, &values); err != nil || values == nil || len(values) > maxEntries {
			return errInput
		}
		for _, value := range values {
			// Paths deliberately use schema names rather than caller-supplied IDs.
			if err := shape(value, typ.Elem(), path+"[]", missing); err != nil {
				return err
			}
		}
	case reflect.String:
		var value string
		if err := json.Unmarshal(data, &value); err != nil || value == "" || len(value) > 256 || strings.TrimSpace(value) != value || strings.ContainsAny(value, "\r\n\x00") {
			return errInput
		}
		if strings.HasSuffix(path, ".contract") && value != Contract {
			return errInput
		}
		if strings.HasSuffix(path, ".class") && !knownClass(value) {
			return errInput
		}
		if strings.HasSuffix(path, ".review_policy") && value != "none" && value != "required" {
			return errInput
		}
	case reflect.Int:
		var value int
		if err := json.Unmarshal(data, &value); err != nil || value < 0 {
			return errInput
		}
		if strings.HasSuffix(path, "_bps") {
			if value > 10000 {
				return errInput
			}
		} else if value == 0 {
			return errInput
		}
	case reflect.Bool:
		var value bool
		if err := json.Unmarshal(data, &value); err != nil {
			return errInput
		}
	default:
		return errInput
	}
	return nil
}

func knownClass(s string) bool {
	switch s {
	case "code", "docs", "research", "review", "metadata":
		return true
	default:
		return false
	}
}
