package routes

import (
	"errors"
	"net/url"
	"testing"
)

func TestMatchBuildsSubjectFromPathAndQuery(t *testing.T) {
	route, err := Compile("/orders/{id}", "orders.{id}.{view}", []string{"GET"}, map[string]Parameter{
		"id":   {Source: "path", Name: "id", Pattern: `^[A-Za-z0-9_-]+$`},
		"view": {Source: "query", Name: "view", Pattern: `^[A-Za-z0-9_-]+$`},
	})
	if err != nil {
		t.Fatal(err)
	}
	subject, matched, err := route.Match("GET", "/orders/order-42", url.Values{"view": {"summary"}})
	if err != nil || !matched || subject != "orders.order-42.summary" {
		t.Fatalf("Match() = %q, %t, %v", subject, matched, err)
	}
}

func TestMatchRejectsAmbiguousOrUnsafeValues(t *testing.T) {
	route, _ := Compile("/orders/{id}", "orders.{id}.{view}", []string{"GET"}, map[string]Parameter{
		"id":   {Source: "path", Name: "id", Pattern: `^[A-Za-z0-9_-]+$`},
		"view": {Source: "query", Name: "view", Pattern: `^[A-Za-z0-9_-]+$`},
	})
	for _, test := range []struct {
		path  string
		query url.Values
	}{
		{"/orders/bad%2Fvalue", url.Values{"view": {"summary"}}},
		{"/orders/good", url.Values{"view": {"one", "two"}}},
	} {
		_, matched, err := route.Match("GET", test.path, test.query)
		if !matched || !errors.Is(err, ErrInvalidParameter) {
			t.Fatalf("Match() = %t, %v", matched, err)
		}
	}
}

func TestMatchRequiresParameterPatternToSpanEntireValue(t *testing.T) {
	route, err := Compile("/items/{id}", "items.{id}.{view}", []string{"GET"}, map[string]Parameter{
		"id":   {Source: "path", Name: "id", Pattern: `^safe|other$`},
		"view": {Source: "query", Name: "view", Pattern: `^summary|detail$`},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		path  string
		query url.Values
	}{
		{path: "/items/safe.admin", query: url.Values{"view": {"summary"}}},
		{path: "/items/safe", query: url.Values{"view": {"summary.admin"}}},
	} {
		_, matched, matchErr := route.Match("GET", test.path, test.query)
		if !matched || !errors.Is(matchErr, ErrInvalidParameter) {
			t.Fatalf("Match(%q, %v) = matched %t, error %v", test.path, test.query, matched, matchErr)
		}
	}
}

func TestMatchDecodesLiteralSegmentsWithoutChangingBoundaries(t *testing.T) {
	route, err := Compile("/café/{id}", "cafes.{id}", []string{"GET"}, map[string]Parameter{
		"id": {Source: "path", Name: "id", Pattern: `^[A-Za-z0-9_-]+$`},
	})
	if err != nil {
		t.Fatal(err)
	}
	subject, matched, err := route.Match("GET", "/caf%C3%A9/order-42", nil)
	if err != nil || !matched || subject != "cafes.order-42" {
		t.Fatalf("Match() = %q, %t, %v", subject, matched, err)
	}
	_, matched, err = route.Match("GET", "/caf%C3%A9/order%2F42", nil)
	if !matched || !errors.Is(err, ErrInvalidParameter) {
		t.Fatalf("encoded slash Match() = matched %t, error %v", matched, err)
	}
}

func FuzzMatchNeverConstructsWildcardSubject(f *testing.F) {
	f.Add("safe", "detail")
	route, _ := Compile("/items/{id}", "items.{id}.{view}", []string{"GET"}, map[string]Parameter{
		"id":   {Source: "path", Name: "id", Pattern: `^[A-Za-z0-9_-]+$`},
		"view": {Source: "query", Name: "view", Pattern: `^[A-Za-z0-9_-]+$`},
	})
	f.Fuzz(func(t *testing.T, id, view string) {
		subject, matched, err := route.Match("GET", "/items/"+url.PathEscape(id), url.Values{"view": {view}})
		if err == nil && matched && (containsRune(subject, '*') || containsRune(subject, '>')) {
			t.Fatalf("unsafe subject %q", subject)
		}
	})
}

func containsRune(value string, target rune) bool {
	for _, r := range value {
		if r == target {
			return true
		}
	}
	return false
}
