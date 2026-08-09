package natswebgateway

import (
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/nats-io/nats.go"
	"golang.org/x/net/http/httpguts"
)

const maxAcceptBytes = 8192

var errNotAcceptable = errors.New("no declared representation is acceptable")
var qualityPattern = regexp.MustCompile(`^(?:0(?:\.[0-9]{0,3})?|1(?:\.0{0,3})?)$`)

type acceptRange struct {
	typeName, subtype string
	parameters        map[string]string
	quality           float64
	specificity       int
	parameterCount    int
	order             int
}

func selectRepresentation(header string, response Response) (string, error) {
	declared := append([]string{response.ContentType}, response.Representations...)
	if !response.NegotiateAccept || strings.TrimSpace(header) == "" {
		return declared[0], nil
	}
	if len(header) > maxAcceptBytes {
		return "", errNotAcceptable
	}
	ranges, err := parseAccept(header)
	if err != nil {
		return "", errNotAcceptable
	}
	bestIndex, bestQuality := -1, -1.0
	for index, representation := range declared {
		mediaType, parameters, _ := mime.ParseMediaType(representation)
		parts := strings.SplitN(mediaType, "/", 2)
		quality, specificity, parameterCount, rangeOrder := -1.0, -1, -1, len(ranges)
		for _, candidate := range ranges {
			if !candidate.matches(parts[0], parts[1], parameters) {
				continue
			}
			if candidate.specificity > specificity ||
				candidate.specificity == specificity && candidate.parameterCount > parameterCount ||
				candidate.specificity == specificity && candidate.parameterCount == parameterCount && candidate.order < rangeOrder {
				quality, specificity, parameterCount, rangeOrder = candidate.quality, candidate.specificity, candidate.parameterCount, candidate.order
			}
		}
		if quality > 0 && quality > bestQuality {
			bestIndex, bestQuality = index, quality
		}
	}
	if bestIndex < 0 {
		return "", errNotAcceptable
	}
	return declared[bestIndex], nil
}

func parseAccept(value string) ([]acceptRange, error) {
	items, err := splitAccept(value)
	if err != nil || len(items) == 0 || len(items) > 64 {
		return nil, errors.New("invalid Accept header")
	}
	ranges := make([]acceptRange, 0, len(items))
	for index, item := range items {
		trimmed := strings.TrimSpace(item)
		segments, splitErr := splitHeaderValue(trimmed, ';')
		if splitErr != nil {
			return nil, splitErr
		}
		quality, mediaEnd := 1.0, len(segments)
		for parameterIndex, segment := range segments[1:] {
			name, rawValue, found := strings.Cut(segment, "=")
			name = strings.ToLower(strings.TrimSpace(name))
			if name != "q" {
				continue
			}
			if !found || !qualityPattern.MatchString(strings.TrimSpace(rawValue)) {
				return nil, errors.New("invalid quality value")
			}
			var parseErr error
			quality, parseErr = strconv.ParseFloat(strings.TrimSpace(rawValue), 64)
			if parseErr != nil {
				return nil, errors.New("invalid quality value")
			}
			mediaEnd = parameterIndex + 1
			for _, extension := range segments[mediaEnd+1:] {
				if err := validateAcceptExtension(extension); err != nil {
					return nil, err
				}
			}
			break
		}
		mediaRange, mediaParameters, parseErr := mime.ParseMediaType(strings.Join(segments[:mediaEnd], ";"))
		if parseErr != nil {
			return nil, parseErr
		}
		parts := strings.SplitN(mediaRange, "/", 2)
		if len(parts) != 2 || parts[0] == "" || parts[1] == "" || parts[0] == "*" && parts[1] != "*" {
			return nil, errors.New("invalid media range")
		}
		specificity := 2
		if parts[1] == "*" {
			specificity = 1
		}
		if parts[0] == "*" {
			specificity = 0
		}
		ranges = append(ranges, acceptRange{
			typeName: parts[0], subtype: parts[1], parameters: mediaParameters,
			quality: quality, specificity: specificity, parameterCount: len(mediaParameters), order: index,
		})
	}
	return ranges, nil
}

func validateAcceptExtension(value string) error {
	name, rawValue, hasValue := strings.Cut(value, "=")
	name = strings.TrimSpace(name)
	if !httpguts.ValidHeaderFieldName(name) || strings.EqualFold(name, "q") {
		return errors.New("invalid Accept extension")
	}
	if !hasValue {
		return nil
	}
	if strings.TrimSpace(rawValue) == "" {
		return errors.New("invalid Accept extension")
	}
	if _, _, err := mime.ParseMediaType("application/octet-stream;" + value); err != nil {
		return errors.New("invalid Accept extension")
	}
	return nil
}

func splitAccept(value string) ([]string, error) {
	return splitHeaderValue(value, ',')
}

func splitHeaderValue(value string, delimiter rune) ([]string, error) {
	var result []string
	start, quoted, escaped := 0, false, false
	for index, character := range value {
		switch {
		case escaped:
			escaped = false
		case quoted && character == '\\':
			escaped = true
		case character == '"':
			quoted = !quoted
		case character == delimiter && !quoted:
			if strings.TrimSpace(value[start:index]) == "" {
				return nil, errors.New("empty media range")
			}
			result = append(result, value[start:index])
			start = index + 1
		}
	}
	if quoted || escaped || strings.TrimSpace(value[start:]) == "" {
		return nil, errors.New("invalid media range")
	}
	return append(result, value[start:]), nil
}

func (mediaRange acceptRange) matches(typeName, subtype string, parameters map[string]string) bool {
	if (mediaRange.typeName != "*" && mediaRange.typeName != typeName) ||
		(mediaRange.subtype != "*" && mediaRange.subtype != subtype) {
		return false
	}
	for name, value := range mediaRange.parameters {
		if parameters[name] != value {
			return false
		}
	}
	return true
}

func validateReply(reply *nats.Msg, response Response, representation string) (int, string, error) {
	serviceError := reply.Header.Values("Nats-Service-Error")
	serviceCode := reply.Header.Values("Nats-Service-Error-Code")
	if len(serviceError) != 0 || len(serviceCode) != 0 {
		if len(serviceError) != 1 || len(serviceCode) != 1 || serviceError[0] == "" || len(serviceError[0]) > 1024 ||
			!httpguts.ValidHeaderFieldValue(serviceError[0]) || !validServiceErrorCode(serviceCode[0]) {
			return 0, "", errors.New("malformed ADR-32 service error")
		}
		status, exists := response.ServiceErrorStatuses[serviceCode[0]]
		if !exists {
			return http.StatusBadGateway, "upstream service error", nil
		}
		return status, "service request failed", nil
	}
	if response.Mode == responseModeJSON && !json.Valid(reply.Data) {
		return 0, "", errors.New("malformed JSON reply")
	}
	if values := reply.Header.Values("Content-Type"); len(values) != 0 {
		if len(values) != 1 {
			return 0, "", errors.New("multiple upstream content types")
		}
		mediaType, parameters, err := mime.ParseMediaType(values[0])
		if err != nil || len(parameters) != 0 || mediaType != representation {
			return 0, "", fmt.Errorf("upstream content type %q does not match negotiated representation", values[0])
		}
	}
	return http.StatusOK, "", nil
}

func writeGatewayError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(struct {
		Error string `json:"error"`
	}{Error: message})
}
