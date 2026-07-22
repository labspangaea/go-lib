package redis

import (
	"encoding/json"
	"testing"
)

func TestWire_JSONRoundTrip(t *testing.T) {
	original := wire{
		ID:      "msg-1",
		Key:     "orders.created",
		Payload: []byte(`{"id":42}`),
		Headers: map[string]string{"trace_id": "abc", "source": "test"},
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded wire
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if decoded.ID != original.ID {
		t.Errorf("ID = %q, want %q", decoded.ID, original.ID)
	}
	if decoded.Key != original.Key {
		t.Errorf("Key = %q, want %q", decoded.Key, original.Key)
	}
	if string(decoded.Payload) != string(original.Payload) {
		t.Errorf("Payload = %q, want %q", decoded.Payload, original.Payload)
	}
	if decoded.Headers["trace_id"] != "abc" {
		t.Errorf("Headers[trace_id] = %q, want abc", decoded.Headers["trace_id"])
	}
	if decoded.Headers["source"] != "test" {
		t.Errorf("Headers[source] = %q, want test", decoded.Headers["source"])
	}
}

func TestWire_JSONRoundTrip_Empty(t *testing.T) {
	original := wire{}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded wire
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if decoded.ID != "" || decoded.Key != "" || decoded.Payload != nil || decoded.Headers != nil {
		t.Errorf("expected zero wire, got %+v", decoded)
	}
}

func TestNewPublisher_NotNil(t *testing.T) {
	// nil client is fine for construction — it panics only on Publish
	p := NewPublisher(nil)
	if p == nil {
		t.Fatal("expected non-nil publisher")
	}
}

func TestNewConsumer_NotNil(t *testing.T) {
	c := NewConsumer(nil)
	if c == nil {
		t.Fatal("expected non-nil consumer")
	}
}
