package domain_test

import (
	"encoding/json"
	"testing"

	"github.com/4thel00z/qdrant-operator/internal/domain"
)

func TestPointIDKeepsLargeIntegersExact(t *testing.T) {
	for _, raw := range []string{"18446744073709551615", "9007199254740993", `"3f2a9c40-0000-4000-8000-000000000001"`} {
		id := domain.PointID{}
		if err := json.Unmarshal([]byte(raw), &id); err != nil {
			t.Fatalf("%s: %v", raw, err)
		}
		encoded, err := json.Marshal(id)
		if err != nil {
			t.Fatalf("%s: %v", raw, err)
		}
		if string(encoded) != raw {
			t.Errorf("round trip of %s gave %s", raw, encoded)
		}
	}
	if domain.ComparePointIDs(domain.NumberPointID(9007199254740992), domain.NumberPointID(9007199254740993)) >= 0 {
		t.Error("neighbouring integers beyond 2^53 must stay ordered")
	}
}

func TestPointIDRejectsWhatQdrantDoesNotAccept(t *testing.T) {
	for _, raw := range []string{"-1", "1.5", "1e3", "true", "[1]", `{"a":1}`} {
		id := domain.PointID{}
		if err := json.Unmarshal([]byte(raw), &id); err == nil {
			t.Errorf("%s: want an error, got %v", raw, id)
		}
	}
}

func TestPointTreatsNullFieldsAsAbsent(t *testing.T) {
	point := domain.Point{}
	if err := json.Unmarshal([]byte(`{"id": 1, "vector": null, "payload": {"a": 1}, "shard_key": null}`), &point); err != nil {
		t.Fatalf("decode: %v", err)
	}
	encoded, err := json.Marshal(point.ToPointStruct())
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if string(encoded) != `{"id":1,"payload":{"a":1}}` {
		t.Errorf("point struct = %s", encoded)
	}
}

func TestDigestIgnoresKeyOrderAndShardKey(t *testing.T) {
	left := domain.Point{ID: domain.NumberPointID(1), Payload: json.RawMessage(`{"a":1,"b":[1.0,2]}`), Vector: json.RawMessage(`[0.1]`)}
	right := domain.Point{
		ID: domain.NumberPointID(1), Payload: json.RawMessage(`{ "b": [1E0, 2], "a": 1 }`),
		Vector: json.RawMessage(`[0.10]`), ShardKey: json.RawMessage(`"eu"`),
	}
	leftDigest, err := left.Digest()
	if err != nil {
		t.Fatalf("digest: %v", err)
	}
	rightDigest, err := right.Digest()
	if err != nil {
		t.Fatalf("digest: %v", err)
	}
	if leftDigest != rightDigest {
		t.Errorf("digests differ: %s %s", leftDigest, rightDigest)
	}
}

func TestDigestReportsMalformedPayload(t *testing.T) {
	point := domain.Point{ID: domain.NumberPointID(1), Payload: json.RawMessage(`{"a":`)}
	if _, err := point.Digest(); err == nil {
		t.Error("want an error for a truncated payload")
	}
}
