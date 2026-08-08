package natswebgateway

import (
	"errors"
	"fmt"
	"mime"
	"net/http"
	"regexp"
	"regexp/syntax"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/caddyconfig/caddyfile"
	"golang.org/x/net/http/httpguts"
)

const (
	responseModeRaw        = "raw"
	streamModeRequestReply = "request_reply"
	streamModeCoreSSE      = "core_sse"
	streamModeJetStreamSSE = "jetstream_sse"
)

var placeholderPattern = regexp.MustCompile(`\{([A-Za-z][A-Za-z0-9_]*)\}`)
var queryNamePattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_.-]*$`)

// Parameter declares the sole HTTP source and accepted grammar for a template value.
type Parameter struct {
	Source  string `json:"source"`
	Name    string `json:"name"`
	Pattern string `json:"pattern"`
}

// Route declares one bounded HTTP-to-NATS operation.
type Route struct {
	Name                string               `json:"name"`
	Path                string               `json:"path"`
	Methods             []string             `json:"methods"`
	Subject             string               `json:"subject"`
	Parameters          map[string]Parameter `json:"parameters,omitempty"`
	RequestHeaders      []string             `json:"request_headers,omitempty"`
	Timeout             caddy.Duration       `json:"timeout"`
	MaxRequestBodyBytes int64                `json:"max_request_body_bytes"`
	MaxReplyBytes       int64                `json:"max_reply_bytes"`
	Response            Response             `json:"response"`
	StreamMode          string               `json:"stream_mode"`
}

// Response declares safe HTTP behavior for a NATS reply.
type Response struct {
	Mode                 string         `json:"mode"`
	Headers              []string       `json:"headers,omitempty"`
	ContentType          string         `json:"content_type,omitempty"`
	ServiceErrorStatuses map[string]int `json:"service_error_statuses,omitempty"`
}

func validateRoutes(routes []Route) error {
	if len(routes) == 0 {
		return errors.New("at least one route is required")
	}
	seenNames := make(map[string]struct{}, len(routes))
	for i := range routes {
		if err := routes[i].validate(); err != nil {
			return fmt.Errorf("route %d: %w", i, err)
		}
		if _, exists := seenNames[routes[i].Name]; exists {
			return fmt.Errorf("route %d: duplicate name %q", i, routes[i].Name)
		}
		seenNames[routes[i].Name] = struct{}{}
		for j := 0; j < i; j++ {
			if methodsOverlap(routes[i].Methods, routes[j].Methods) && pathsOverlap(routes[i].Path, routes[j].Path) {
				return fmt.Errorf("routes %q and %q have overlapping paths and methods", routes[j].Name, routes[i].Name)
			}
		}
	}
	return nil
}

func (route Route) validate() error {
	if !validName(route.Name) {
		return errors.New("name must contain only letters, digits, '_' or '-' and start with a letter")
	}
	pathParams, err := validatePath(route.Path)
	if err != nil {
		return err
	}
	if err := validateMethods(route.Methods); err != nil {
		return err
	}
	subjectParams, err := validateSubject(route.Subject)
	if err != nil {
		return err
	}
	usedParams := append(pathParams, subjectParams...)
	if err := validateParameters(route.Parameters, pathParams, usedParams); err != nil {
		return err
	}
	if err := validateHeaderList("request_headers", route.RequestHeaders); err != nil {
		return err
	}
	if time.Duration(route.Timeout) <= 0 {
		return errors.New("timeout must be greater than zero")
	}
	if route.MaxRequestBodyBytes <= 0 {
		return errors.New("max_request_body_bytes must be greater than zero")
	}
	if route.MaxReplyBytes <= 0 {
		return errors.New("max_reply_bytes must be greater than zero")
	}
	if route.StreamMode != streamModeRequestReply && route.StreamMode != streamModeCoreSSE && route.StreamMode != streamModeJetStreamSSE {
		return fmt.Errorf("unsupported stream_mode %q", route.StreamMode)
	}
	if route.Response.Mode != responseModeRaw {
		return fmt.Errorf("unsupported response mode %q", route.Response.Mode)
	}
	if err := validateHeaderList("response.headers", route.Response.Headers); err != nil {
		return err
	}
	if route.Response.ContentType != "" {
		if strings.ContainsAny(route.Response.ContentType, "\r\n") {
			return errors.New("response.content_type must be a valid media type without line breaks")
		}
		if _, _, err := mime.ParseMediaType(route.Response.ContentType); err != nil {
			return fmt.Errorf("response.content_type: %w", err)
		}
	}
	for code, status := range route.Response.ServiceErrorStatuses {
		if !validServiceErrorCode(code) {
			return fmt.Errorf("response.service_error_statuses contains invalid ADR-32 code %q", code)
		}
		if status < http.StatusBadRequest || status > 599 {
			return fmt.Errorf("response.service_error_statuses[%q] must be between 400 and 599", code)
		}
	}
	return nil
}

func validName(value string) bool {
	for i, r := range value {
		if i == 0 && !unicode.IsLetter(r) {
			return false
		}
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' && r != '-' {
			return false
		}
	}
	return value != ""
}

func validatePath(path string) ([]string, error) {
	if path == "" || path[0] != '/' || strings.ContainsAny(path, "?#") {
		return nil, errors.New("path must be an absolute path without a query or fragment")
	}
	if path != "/" && strings.HasSuffix(path, "/") {
		return nil, errors.New("path must not have a trailing slash")
	}
	if strings.Contains(path, "//") || strings.ContainsAny(path, "*\\ \t\r\n") {
		return nil, errors.New("path must contain non-empty segments without wildcards, backslashes, or whitespace")
	}
	return validateTemplate("path", path, true)
}

func validateSubject(subject string) ([]string, error) {
	if subject == "" || strings.HasPrefix(subject, ".") || strings.HasSuffix(subject, ".") || strings.Contains(subject, "..") {
		return nil, errors.New("subject must contain non-empty dot-separated tokens")
	}
	if strings.ContainsAny(subject, "*> \t\r\n") {
		return nil, errors.New("subject must not contain wildcards or whitespace")
	}
	return validateTemplate("subject", subject, false)
}

func validateTemplate(field, value string, path bool) ([]string, error) {
	matches := placeholderPattern.FindAllStringSubmatchIndex(value, -1)
	without := placeholderPattern.ReplaceAllString(value, "")
	if strings.ContainsAny(without, "{}") {
		return nil, fmt.Errorf("%s contains a malformed placeholder", field)
	}
	params := make([]string, 0, len(matches))
	for _, match := range matches {
		name := value[match[2]:match[3]]
		if slices.Contains(params, name) {
			return nil, fmt.Errorf("%s repeats parameter %q", field, name)
		}
		if path {
			start, end := match[0], match[1]
			if (start > 0 && value[start-1] != '/') || (end < len(value) && value[end] != '/') {
				return nil, fmt.Errorf("path parameter %q must occupy a complete segment", name)
			}
		} else {
			start, end := match[0], match[1]
			if (start > 0 && value[start-1] != '.') || (end < len(value) && value[end] != '.') {
				return nil, fmt.Errorf("subject parameter %q must occupy a complete token", name)
			}
		}
		params = append(params, name)
	}
	return params, nil
}

func validateParameters(parameters map[string]Parameter, pathParams, used []string) error {
	for _, name := range used {
		parameter, ok := parameters[name]
		if !ok {
			return fmt.Errorf("parameter %q requires an explicit source and validation expression", name)
		}
		if parameter.Source != "path" && parameter.Source != "query" {
			return fmt.Errorf("parameter %q has unsupported source %q", name, parameter.Source)
		}
		if parameter.Name == "" {
			return fmt.Errorf("parameter %q requires an HTTP source name", name)
		}
		if parameter.Source == "query" && !queryNamePattern.MatchString(parameter.Name) {
			return fmt.Errorf("parameter %q has invalid query source name %q", name, parameter.Name)
		}
		if parameter.Source == "path" && (!slices.Contains(pathParams, name) || parameter.Name != name) {
			return fmt.Errorf("parameter %q path source must name the matching path placeholder", name)
		}
		if parameter.Source == "query" && slices.Contains(pathParams, name) {
			return fmt.Errorf("path placeholder %q must use a path source", name)
		}
		expression := parameter.Pattern
		if expression == "" || !strings.HasPrefix(expression, "^") || !strings.HasSuffix(expression, "$") {
			return fmt.Errorf("parameter %q validation expression must be explicitly anchored", name)
		}
		if err := validateSafeParameterPattern(expression); err != nil {
			return fmt.Errorf("parameter %q validation expression: %w", name, err)
		}
	}
	for name := range parameters {
		if !slices.Contains(used, name) {
			return fmt.Errorf("parameter %q is not used by path or subject", name)
		}
	}
	return nil
}

func validateSafeParameterPattern(expression string) error {
	parsed, err := syntax.Parse(expression, syntax.Perl)
	if err != nil {
		return err
	}
	minimum, err := safePatternMinimumLength(parsed)
	if err != nil {
		return err
	}
	if minimum == 0 {
		return errors.New("must not match an empty value")
	}
	return nil
}

func safePatternMinimumLength(expression *syntax.Regexp) (int, error) {
	switch expression.Op {
	case syntax.OpNoMatch:
		return 0, errors.New("must match at least one value")
	case syntax.OpEmptyMatch, syntax.OpBeginLine, syntax.OpEndLine, syntax.OpBeginText, syntax.OpEndText, syntax.OpWordBoundary, syntax.OpNoWordBoundary:
		return 0, nil
	case syntax.OpLiteral:
		if expression.Flags&syntax.FoldCase != 0 {
			return 0, errors.New("case-folded literals are not supported")
		}
		for _, value := range expression.Rune {
			if !safeParameterRune(value) {
				return 0, fmt.Errorf("permits unsafe character %q", value)
			}
		}
		return len(expression.Rune), nil
	case syntax.OpCharClass:
		for index := 0; index < len(expression.Rune); index += 2 {
			for value := expression.Rune[index]; value <= expression.Rune[index+1]; value++ {
				if !safeParameterRune(value) {
					return 0, fmt.Errorf("permits unsafe character %q", value)
				}
			}
		}
		return 1, nil
	case syntax.OpCapture:
		return safePatternMinimumLength(expression.Sub[0])
	case syntax.OpConcat:
		minimum := 0
		for _, child := range expression.Sub {
			childMinimum, err := safePatternMinimumLength(child)
			if err != nil {
				return 0, err
			}
			minimum += childMinimum
		}
		return minimum, nil
	case syntax.OpAlternate:
		minimum := -1
		for _, child := range expression.Sub {
			childMinimum, err := safePatternMinimumLength(child)
			if err != nil {
				return 0, err
			}
			if minimum == -1 || childMinimum < minimum {
				minimum = childMinimum
			}
		}
		return minimum, nil
	case syntax.OpQuest, syntax.OpStar:
		if _, err := safePatternMinimumLength(expression.Sub[0]); err != nil {
			return 0, err
		}
		return 0, nil
	case syntax.OpPlus:
		return safePatternMinimumLength(expression.Sub[0])
	case syntax.OpRepeat:
		minimum, err := safePatternMinimumLength(expression.Sub[0])
		if err != nil {
			return 0, err
		}
		return expression.Min * minimum, nil
	default:
		return 0, fmt.Errorf("uses unsupported regexp operation %s", expression.Op)
	}
}

func safeParameterRune(value rune) bool {
	return value >= 'A' && value <= 'Z' || value >= 'a' && value <= 'z' ||
		value >= '0' && value <= '9' || value == '_' || value == '-'
}

func validateMethods(methods []string) error {
	if len(methods) == 0 {
		return errors.New("at least one method is required")
	}
	seen := make(map[string]struct{}, len(methods))
	for _, method := range methods {
		if method == "" || method != strings.ToUpper(method) || !httpguts.ValidHeaderFieldName(method) {
			return fmt.Errorf("invalid HTTP method %q", method)
		}
		if _, exists := seen[method]; exists {
			return fmt.Errorf("duplicate HTTP method %q", method)
		}
		seen[method] = struct{}{}
	}
	return nil
}

func validateHeaderList(field string, headers []string) error {
	seen := make(map[string]struct{}, len(headers))
	for _, header := range headers {
		canonical := http.CanonicalHeaderKey(header)
		if !httpguts.ValidHeaderFieldName(header) || canonical != header {
			return fmt.Errorf("%s contains non-canonical or invalid header %q", field, header)
		}
		if forbiddenHeader(canonical) {
			return fmt.Errorf("%s contains forbidden header %q", field, header)
		}
		if _, exists := seen[canonical]; exists {
			return fmt.Errorf("%s contains duplicate header %q", field, header)
		}
		seen[canonical] = struct{}{}
	}
	return nil
}

func forbiddenHeader(header string) bool {
	switch header {
	case "Authorization", "Proxy-Authorization", "Cookie", "Set-Cookie", "Connection", "Content-Length", "Host", "Keep-Alive", "Proxy-Authenticate", "Proxy-Connection", "Te", "Trailer", "Transfer-Encoding", "Upgrade":
		return true
	}
	lower := strings.ToLower(header)
	return strings.HasPrefix(lower, "nats-") || strings.HasPrefix(lower, "x-nats-") ||
		strings.HasPrefix(lower, "x-authenticated-") || strings.HasPrefix(lower, "x-user-") ||
		strings.HasPrefix(lower, "x-tenant-")
}

func validServiceErrorCode(code string) bool {
	if code == "" || len(code) > 10 || (len(code) > 1 && code[0] == '0') {
		return false
	}
	value, err := strconv.ParseUint(code, 10, 64)
	return err == nil && value > 0
}

func methodsOverlap(left, right []string) bool {
	for _, method := range left {
		if slices.Contains(right, method) {
			return true
		}
	}
	return false
}

func pathsOverlap(left, right string) bool {
	leftSegments := strings.Split(strings.TrimPrefix(left, "/"), "/")
	rightSegments := strings.Split(strings.TrimPrefix(right, "/"), "/")
	if len(leftSegments) != len(rightSegments) {
		return false
	}
	for i := range leftSegments {
		leftParam := placeholderPattern.MatchString(leftSegments[i])
		rightParam := placeholderPattern.MatchString(rightSegments[i])
		if !leftParam && !rightParam && leftSegments[i] != rightSegments[i] {
			return false
		}
	}
	return true
}

// UnmarshalCaddyfile adapts nats_web_gateway route blocks into JSON-equivalent configuration.
func (h *Handler) UnmarshalCaddyfile(d *caddyfile.Dispenser) error {
	d.Next()
	if d.CountRemainingArgs() != 0 {
		return d.ArgErr()
	}
	for d.NextBlock(0) {
		if d.Val() != "route" {
			return d.Errf("unrecognized subdirective %q", d.Val())
		}
		route, err := unmarshalRoute(d.NewFromNextSegment())
		if err != nil {
			return err
		}
		h.Routes = append(h.Routes, route)
	}
	return h.Validate()
}

func unmarshalRoute(d *caddyfile.Dispenser) (Route, error) {
	var route Route
	seenOptions := make(map[string]struct{})
	d.Next()
	if !d.Args(&route.Name) {
		return route, d.ArgErr()
	}
	for d.NextBlock(0) {
		if d.Val() != "parameter" && d.Val() != "service_error_status" {
			if _, exists := seenOptions[d.Val()]; exists {
				return route, d.Errf("route option %q already specified", d.Val())
			}
			seenOptions[d.Val()] = struct{}{}
		}
		switch d.Val() {
		case "path":
			if !d.AllArgs(&route.Path) {
				return route, d.ArgErr()
			}
		case "methods":
			route.Methods = d.RemainingArgs()
			if len(route.Methods) == 0 {
				return route, d.ArgErr()
			}
		case "subject":
			if !d.AllArgs(&route.Subject) {
				return route, d.ArgErr()
			}
		case "parameter":
			var name, source, sourceName, expression string
			if !d.AllArgs(&name, &source, &sourceName, &expression) {
				return route, d.ArgErr()
			}
			if route.Parameters == nil {
				route.Parameters = make(map[string]Parameter)
			}
			if _, exists := route.Parameters[name]; exists {
				return route, d.Errf("parameter %q already specified", name)
			}
			route.Parameters[name] = Parameter{Source: source, Name: sourceName, Pattern: expression}
		case "request_headers":
			route.RequestHeaders = d.RemainingArgs()
		case "timeout":
			var value string
			if !d.AllArgs(&value) {
				return route, d.ArgErr()
			}
			duration, err := caddy.ParseDuration(value)
			if err != nil {
				return route, d.WrapErr(err)
			}
			route.Timeout = caddy.Duration(duration)
		case "max_request_body_bytes":
			if err := parsePositiveInt64(d, &route.MaxRequestBodyBytes); err != nil {
				return route, err
			}
		case "max_reply_bytes":
			if err := parsePositiveInt64(d, &route.MaxReplyBytes); err != nil {
				return route, err
			}
		case "stream_mode":
			if !d.AllArgs(&route.StreamMode) {
				return route, d.ArgErr()
			}
		case "response_mode":
			if !d.AllArgs(&route.Response.Mode) {
				return route, d.ArgErr()
			}
		case "response_headers":
			route.Response.Headers = d.RemainingArgs()
		case "response_content_type":
			if !d.AllArgs(&route.Response.ContentType) {
				return route, d.ArgErr()
			}
		case "service_error_status":
			var code, statusValue string
			if !d.AllArgs(&code, &statusValue) {
				return route, d.ArgErr()
			}
			status, err := strconv.Atoi(statusValue)
			if err != nil {
				return route, d.Errf("invalid HTTP status %q", statusValue)
			}
			if route.Response.ServiceErrorStatuses == nil {
				route.Response.ServiceErrorStatuses = make(map[string]int)
			}
			if _, exists := route.Response.ServiceErrorStatuses[code]; exists {
				return route, d.Errf("service error code %q already specified", code)
			}
			route.Response.ServiceErrorStatuses[code] = status
		default:
			return route, d.Errf("unrecognized route option %q", d.Val())
		}
	}
	return route, nil
}

func parsePositiveInt64(d *caddyfile.Dispenser, target *int64) error {
	var value string
	if !d.AllArgs(&value) {
		return d.ArgErr()
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return d.Errf("invalid byte limit %q", value)
	}
	*target = parsed
	return nil
}
