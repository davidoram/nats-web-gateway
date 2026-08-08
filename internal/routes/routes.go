// Package routes contains declared-route enforcement and subject construction.
package routes

import (
	"errors"
	"net/url"
	"regexp"
	"strings"
)

var ErrInvalidParameter = errors.New("invalid route parameter")

type Parameter struct {
	Source, Name, Pattern string
}

type Route struct {
	Path, Subject string
	Methods       []string
	Parameters    map[string]Parameter
	patterns      map[string]*regexp.Regexp
}

func Compile(path, subject string, methods []string, parameters map[string]Parameter) (Route, error) {
	r := Route{Path: path, Subject: subject, Methods: methods, Parameters: parameters, patterns: make(map[string]*regexp.Regexp, len(parameters))}
	for name, parameter := range parameters {
		pattern, err := regexp.Compile(parameter.Pattern)
		if err != nil {
			return Route{}, err
		}
		r.patterns[name] = pattern
	}
	return r, nil
}

// Match returns whether the declared path and method match and, if so, the
// validated subject. A matching path with invalid input returns an error.
func (r Route) Match(method, escapedPath string, query url.Values) (string, bool, error) {
	if !contains(r.Methods, method) {
		return "", false, nil
	}
	want, got := segments(r.Path), segments(escapedPath)
	if len(want) != len(got) {
		return "", false, nil
	}
	values := make(map[string]string, len(r.Parameters))
	for i := range want {
		name, placeholder := placeholderName(want[i])
		if !placeholder {
			if want[i] != got[i] {
				return "", false, nil
			}
			continue
		}
		value, err := url.PathUnescape(got[i])
		if err != nil || !r.patterns[name].MatchString(value) {
			return "", true, ErrInvalidParameter
		}
		values[name] = value
	}
	for name, parameter := range r.Parameters {
		if parameter.Source != "query" {
			continue
		}
		items, ok := query[parameter.Name]
		if !ok || len(items) != 1 || !r.patterns[name].MatchString(items[0]) {
			return "", true, ErrInvalidParameter
		}
		values[name] = items[0]
	}
	subject := r.Subject
	for name, value := range values {
		subject = strings.ReplaceAll(subject, "{"+name+"}", value)
	}
	return subject, true, nil
}

func segments(path string) []string {
	if path == "/" {
		return nil
	}
	return strings.Split(strings.TrimPrefix(path, "/"), "/")
}

func placeholderName(segment string) (string, bool) {
	if len(segment) > 2 && segment[0] == '{' && segment[len(segment)-1] == '}' {
		return segment[1 : len(segment)-1], true
	}
	return "", false
}

func contains(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}
