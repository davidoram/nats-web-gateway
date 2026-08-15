// Package credentials contains mechanism-specific adapters that translate
// supported HTTP credential presentations into NATS client authentication
// options.
//
// Adapters do not authenticate identities, inspect JWT claims, or make
// authorization decisions. NATS connection establishment is the authentication
// result and NATS account and subject permissions remain the authorization
// authority.
package credentials
