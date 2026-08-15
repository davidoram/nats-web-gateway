package main

import (
	"errors"
	"reflect"
	"sync"
	"testing"
)

func TestPetStoreCRUDAndStableList(t *testing.T) {
	t.Parallel()
	store := newPetStore()

	if _, err := store.create(pet{ID: "pet-b", Name: "Bramble"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.create(pet{ID: "pet-a", Name: "Aster"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.create(pet{ID: "pet-a", Name: "Duplicate"}); err == nil {
		t.Fatal("duplicate create succeeded")
	}
	if got := store.list(); !reflect.DeepEqual(got, []pet{{ID: "pet-a", Name: "Aster"}, {ID: "pet-b", Name: "Bramble"}}) {
		t.Fatalf("list = %#v", got)
	}
	updated, err := store.update("pet-a", pet{Name: "Aspen"})
	if err != nil || updated != (pet{ID: "pet-a", Name: "Aspen"}) {
		t.Fatalf("update = %#v, %v", updated, err)
	}
	deleted, err := store.delete("pet-a")
	if err != nil || deleted != updated {
		t.Fatalf("delete = %#v, %v", deleted, err)
	}
	if _, err := store.get("pet-a"); !errors.Is(err, errMissingPet) {
		t.Fatalf("get deleted error = %v", err)
	}
}

func TestPetStoreConcurrentAccess(t *testing.T) {
	t.Parallel()
	store := newPetStore()
	var group sync.WaitGroup
	for i := 0; i < 32; i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			_, _ = store.create(pet{ID: "shared", Name: "Pet"})
			_, _ = store.get("shared")
			_ = store.list()
		}()
	}
	group.Wait()
	if got := store.list(); len(got) != 1 {
		t.Fatalf("concurrent list length = %d, want 1", len(got))
	}
}

func TestPetValidationAndDecoding(t *testing.T) {
	t.Parallel()
	for _, value := range []pet{{}, {ID: "id"}, {Name: "name"}} {
		if !errors.Is(validatePet(value), errInvalidPet) {
			t.Fatalf("validatePet(%#v) did not return invalid pet", value)
		}
	}
	var value pet
	if err := decodeJSON([]byte(`{"id":"pet-1","name":"Milo"}`), &value); err != nil {
		t.Fatal(err)
	}
	if value != (pet{ID: "pet-1", Name: "Milo"}) {
		t.Fatalf("decoded pet = %#v", value)
	}
	if err := decodeJSON([]byte(`{"id":`), &value); !errors.Is(err, errInvalidPet) {
		t.Fatalf("malformed JSON error = %v", err)
	}
	id, err := decodeID([]byte(`{"id":"pet-1"}`))
	if err != nil || id != "pet-1" {
		t.Fatalf("decode ID = %q, %v", id, err)
	}
}
