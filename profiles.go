package natswebgateway

import (
	"fmt"
	"slices"
)

var shareableRouteFields = []string{
	"subject", "parameters", "request_headers", "timeout", "max_request_body_bytes",
	"max_reply_bytes", "response", "stream_mode", "core_sse", "security_context",
	"response.mode", "response.headers", "response.content_type", "response.representations",
	"response.negotiate_accept", "response.service_error_statuses",
	"core_sse.buffer_messages", "core_sse.buffer_bytes", "core_sse.heartbeat_interval",
	"core_sse.max_duration", "core_sse.max_connections", "security_context.mechanism",
	"security_context.max_credential_bytes", "security_context.max_connections",
	"security_context.idle_timeout", "security_context.max_lifetime", "security_context.downstream_identity",
	"security_context.downstream_identity.source", "security_context.downstream_identity.header",
	"security_context.downstream_identity.max_value_bytes",
}

func (h Handler) resolvedRoutes() ([]Route, error) {
	profiles := make(map[string]RouteProfile, len(h.RouteProfiles))
	for i, profile := range h.RouteProfiles {
		if !validName(profile.Name) {
			return nil, fmt.Errorf("route_profile %d: invalid name %q", i, profile.Name)
		}
		if _, exists := profiles[profile.Name]; exists {
			return nil, fmt.Errorf("route_profile %d: duplicate name %q", i, profile.Name)
		}
		if profile.Route.Name != "" || profile.Path != "" || len(profile.Methods) != 0 || profile.Profile != "" {
			return nil, fmt.Errorf("route_profile %q: name, path, methods, and profile are route-specific", profile.Name)
		}
		profiles[profile.Name] = profile
	}
	cache := make(map[string]Route, len(profiles))
	visiting := make(map[string]bool, len(profiles))
	var resolveProfile func(string) (Route, error)
	resolveProfile = func(name string) (Route, error) {
		if result, ok := cache[name]; ok {
			return result, nil
		}
		profile, ok := profiles[name]
		if !ok {
			return Route{}, fmt.Errorf("unknown route_profile %q", name)
		}
		if visiting[name] {
			return Route{}, fmt.Errorf("route_profile inheritance cycle at %q", name)
		}
		visiting[name] = true
		var result Route
		var err error
		if profile.Extends != "" {
			result, err = resolveProfile(profile.Extends)
			if err != nil {
				return Route{}, err
			}
		}
		result, err = mergeRoute(result, profile.Route)
		if err != nil {
			return Route{}, fmt.Errorf("route_profile %q: %w", name, err)
		}
		visiting[name] = false
		cache[name] = result
		return result, nil
	}
	for name := range profiles {
		if _, err := resolveProfile(name); err != nil {
			return nil, err
		}
	}
	resolved := make([]Route, len(h.Routes))
	for i, route := range h.Routes {
		base := Route{}
		var err error
		if route.Profile != "" {
			base, err = resolveProfile(route.Profile)
			if err != nil {
				return nil, fmt.Errorf("route %q: %w", route.Name, err)
			}
		}
		base.Name, base.Path, base.Methods = route.Name, route.Path, slices.Clone(route.Methods)
		resolved[i], err = mergeRoute(base, route)
		if err != nil {
			return nil, fmt.Errorf("route %q: %w", route.Name, err)
		}
		resolved[i].Profile, resolved[i].Clear, resolved[i].Extend = "", nil, nil
	}
	return resolved, nil
}

func mergeRoute(base, override Route) (Route, error) {
	cleared := make(map[string]struct{}, len(override.Clear))
	for _, field := range override.Clear {
		if !slices.Contains(shareableRouteFields, field) {
			return Route{}, fmt.Errorf("cannot clear unknown or route-specific field %q", field)
		}
		if _, exists := cleared[field]; exists {
			return Route{}, fmt.Errorf("cannot clear field %q more than once", field)
		}
		cleared[field] = struct{}{}
		clearRouteField(&base, field)
	}
	if override.Subject != "" {
		base.Subject = override.Subject
	}
	if override.Parameters != nil {
		base.Parameters = cloneParameters(override.Parameters)
	}
	if override.RequestHeaders != nil {
		base.RequestHeaders = slices.Clone(override.RequestHeaders)
	}
	if override.Timeout != 0 {
		base.Timeout = override.Timeout
	}
	if override.MaxRequestBodyBytes != 0 {
		base.MaxRequestBodyBytes = override.MaxRequestBodyBytes
	}
	if override.MaxReplyBytes != 0 {
		base.MaxReplyBytes = override.MaxReplyBytes
	}
	mergeResponse(&base.Response, override.Response)
	if override.StreamMode != "" {
		base.StreamMode = override.StreamMode
	}
	if override.CoreSSE != nil {
		if base.CoreSSE == nil {
			base.CoreSSE = new(CoreSSE)
		}
		mergeCoreSSE(base.CoreSSE, *override.CoreSSE)
	}
	if override.SecurityContext != nil {
		if base.SecurityContext == nil {
			base.SecurityContext = new(SecurityContext)
		}
		mergeSecurityContext(base.SecurityContext, *override.SecurityContext)
	}
	if override.Name != "" {
		base.Name = override.Name
	}
	if override.Path != "" {
		base.Path = override.Path
	}
	if override.Methods != nil {
		base.Methods = slices.Clone(override.Methods)
	}
	if override.Extend != nil {
		if err := extendRoute(&base, *override.Extend); err != nil {
			return Route{}, err
		}
	}
	return base, nil
}

func clearRouteField(route *Route, field string) {
	switch field {
	case "subject":
		route.Subject = ""
	case "parameters":
		route.Parameters = nil
	case "request_headers":
		route.RequestHeaders = []string{}
	case "timeout":
		route.Timeout = 0
	case "max_request_body_bytes":
		route.MaxRequestBodyBytes = 0
	case "max_reply_bytes":
		route.MaxReplyBytes = 0
	case "response":
		route.Response = Response{}
	case "stream_mode":
		route.StreamMode = ""
	case "core_sse":
		route.CoreSSE = nil
	case "security_context":
		route.SecurityContext = nil
	case "response.mode":
		route.Response.Mode = ""
	case "response.headers":
		route.Response.Headers = []string{}
	case "response.content_type":
		route.Response.ContentType = ""
	case "response.representations":
		route.Response.Representations = []string{}
	case "response.negotiate_accept":
		route.Response.NegotiateAccept = false
	case "response.service_error_statuses":
		route.Response.ServiceErrorStatuses = map[string]int{}
	case "core_sse.buffer_messages":
		ensureCoreSSE(route).BufferMessages = 0
	case "core_sse.buffer_bytes":
		ensureCoreSSE(route).BufferBytes = 0
	case "core_sse.heartbeat_interval":
		ensureCoreSSE(route).HeartbeatInterval = 0
	case "core_sse.max_duration":
		ensureCoreSSE(route).MaxDuration = 0
	case "core_sse.max_connections":
		ensureCoreSSE(route).MaxConnections = 0
	case "security_context.mechanism":
		ensureSecurityContext(route).Mechanism = ""
	case "security_context.max_credential_bytes":
		ensureSecurityContext(route).MaxCredentialBytes = 0
	case "security_context.max_connections":
		ensureSecurityContext(route).MaxConnections = 0
	case "security_context.idle_timeout":
		ensureSecurityContext(route).IdleTimeout = 0
	case "security_context.max_lifetime":
		ensureSecurityContext(route).MaxLifetime = 0
	case "security_context.downstream_identity":
		ensureSecurityContext(route).DownstreamIdentity = nil
	case "security_context.downstream_identity.source":
		ensureDownstreamIdentity(route).Source = ""
	case "security_context.downstream_identity.header":
		ensureDownstreamIdentity(route).Header = ""
	case "security_context.downstream_identity.max_value_bytes":
		ensureDownstreamIdentity(route).MaxValueBytes = 0
	}
}

func mergeResponse(base *Response, override Response) {
	if override.Mode != "" {
		base.Mode = override.Mode
	}
	if override.Headers != nil {
		base.Headers = slices.Clone(override.Headers)
	}
	if override.ContentType != "" {
		base.ContentType = override.ContentType
	}
	if override.Representations != nil {
		base.Representations = slices.Clone(override.Representations)
	}
	if override.NegotiateAccept {
		base.NegotiateAccept = true
	}
	if override.ServiceErrorStatuses != nil {
		base.ServiceErrorStatuses = cloneResponse(override).ServiceErrorStatuses
	}
}
func mergeCoreSSE(base *CoreSSE, override CoreSSE) {
	if override.BufferMessages != 0 {
		base.BufferMessages = override.BufferMessages
	}
	if override.BufferBytes != 0 {
		base.BufferBytes = override.BufferBytes
	}
	if override.HeartbeatInterval != 0 {
		base.HeartbeatInterval = override.HeartbeatInterval
	}
	if override.MaxDuration != 0 {
		base.MaxDuration = override.MaxDuration
	}
	if override.MaxConnections != 0 {
		base.MaxConnections = override.MaxConnections
	}
}
func mergeSecurityContext(base *SecurityContext, override SecurityContext) {
	if override.Mechanism != "" {
		base.Mechanism = override.Mechanism
	}
	if override.MaxCredentialBytes != 0 {
		base.MaxCredentialBytes = override.MaxCredentialBytes
	}
	if override.MaxConnections != 0 {
		base.MaxConnections = override.MaxConnections
	}
	if override.IdleTimeout != 0 {
		base.IdleTimeout = override.IdleTimeout
	}
	if override.MaxLifetime != 0 {
		base.MaxLifetime = override.MaxLifetime
	}
	if override.DownstreamIdentity != nil {
		if base.DownstreamIdentity == nil {
			base.DownstreamIdentity = new(DownstreamIdentity)
		}
		if override.DownstreamIdentity.Source != "" {
			base.DownstreamIdentity.Source = override.DownstreamIdentity.Source
		}
		if override.DownstreamIdentity.Header != "" {
			base.DownstreamIdentity.Header = override.DownstreamIdentity.Header
		}
		if override.DownstreamIdentity.MaxValueBytes != 0 {
			base.DownstreamIdentity.MaxValueBytes = override.DownstreamIdentity.MaxValueBytes
		}
	}
}
func extendRoute(route *Route, ext RouteExtensions) error {
	if route.Parameters == nil {
		route.Parameters = map[string]Parameter{}
	}
	for k, v := range ext.Parameters {
		if _, exists := route.Parameters[k]; exists {
			return fmt.Errorf("cannot extend parameters with duplicate key %q", k)
		}
		route.Parameters[k] = v
	}
	route.RequestHeaders = append(route.RequestHeaders, ext.RequestHeaders...)
	route.Response.Headers = append(route.Response.Headers, ext.ResponseHeaders...)
	route.Response.Representations = append(route.Response.Representations, ext.Representations...)
	if route.Response.ServiceErrorStatuses == nil {
		route.Response.ServiceErrorStatuses = map[string]int{}
	}
	for k, v := range ext.ServiceErrorStatuses {
		if _, exists := route.Response.ServiceErrorStatuses[k]; exists {
			return fmt.Errorf("cannot extend service_error_statuses with duplicate key %q", k)
		}
		route.Response.ServiceErrorStatuses[k] = v
	}
	return nil
}
func cloneParameters(in map[string]Parameter) map[string]Parameter {
	out := make(map[string]Parameter, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
func cloneResponse(in Response) Response {
	in.Headers = slices.Clone(in.Headers)
	in.Representations = slices.Clone(in.Representations)
	if in.ServiceErrorStatuses != nil {
		m := map[string]int{}
		for k, v := range in.ServiceErrorStatuses {
			m[k] = v
		}
		in.ServiceErrorStatuses = m
	}
	return in
}
