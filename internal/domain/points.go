package domain

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"math/big"
	"slices"
	"strconv"
	"strings"
	"unicode/utf16"
)

type pointIDKind int

const (
	pointIDNone pointIDKind = iota
	pointIDNumber
	pointIDText
)

// PointID is a Qdrant point ID, an unsigned integer or a UUID string. The zero value is no ID.
type PointID struct {
	kind  pointIDKind
	value string
}

func NumberPointID(number uint64) PointID {
	return PointID{kind: pointIDNumber, value: strconv.FormatUint(number, 10)}
}

func TextPointID(text string) PointID {
	return PointID{kind: pointIDText, value: text}
}

func (id PointID) IsZero() bool {
	return id.kind == pointIDNone
}

func (id PointID) IsNumber() bool {
	return id.kind == pointIDNumber
}

func (id PointID) String() string {
	return id.value
}

func (id PointID) MarshalJSON() ([]byte, error) {
	if id.kind == pointIDNone {
		return []byte("null"), nil
	}
	if id.kind == pointIDNumber {
		return []byte(id.value), nil
	}
	return json.Marshal(id.value)
}

func (id *PointID) UnmarshalJSON(data []byte) error {
	decoded, err := decodeNumbers(data)
	if err != nil {
		return fmt.Errorf("point id: %w", err)
	}
	parsed, err := pointIDFromValue(decoded)
	if err != nil {
		return err
	}
	*id = parsed
	return nil
}

func pointIDFromValue(value any) (PointID, error) {
	switch typed := value.(type) {
	case nil:
		return PointID{}, nil
	case string:
		return TextPointID(typed), nil
	case json.Number:
		digits, err := unsignedDigits(typed)
		if err != nil {
			return PointID{}, fmt.Errorf("point id %s is not an unsigned integer or a string", typed)
		}
		return PointID{kind: pointIDNumber, value: digits}, nil
	}
	return PointID{}, fmt.Errorf("point id %v is not an unsigned integer or a string", value)
}

// unsignedDigits returns the canonical decimal digits of a JSON integer, or an error for floats and negatives.
func unsignedDigits(number json.Number) (string, error) {
	if strings.ContainsAny(number.String(), ".eE") {
		return "", fmt.Errorf("%s is not an integer", number)
	}
	parsed, ok := new(big.Int).SetString(number.String(), 10)
	if !ok || parsed.Sign() < 0 {
		return "", fmt.Errorf("%s is not an unsigned integer", number)
	}
	return parsed.String(), nil
}

// ComparePointIDs follows Qdrant's scroll order: integer IDs ascending, then UUIDs.
func ComparePointIDs(left PointID, right PointID) int {
	if left.kind != right.kind {
		return int(left.kind) - int(right.kind)
	}
	if left.kind == pointIDNumber && len(left.value) != len(right.value) {
		return len(left.value) - len(right.value)
	}
	return strings.Compare(left.value, right.value)
}

type PointShardKeyKind int

const (
	ShardKeyAutomatic PointShardKeyKind = iota
	ShardKeyText
	ShardKeyNumber
)

// PointShardKey is the custom shard key an upsert is routed by; the zero value lets the target choose.
type PointShardKey struct {
	Kind   PointShardKeyKind
	Text   string
	Number uint64
}

func TextShardKey(text string) PointShardKey {
	return PointShardKey{Kind: ShardKeyText, Text: text}
}

func NumberShardKey(number uint64) PointShardKey {
	return PointShardKey{Kind: ShardKeyNumber, Number: number}
}

func (k PointShardKey) IsSet() bool {
	return k.Kind != ShardKeyAutomatic
}

func (k PointShardKey) MarshalJSON() ([]byte, error) {
	if k.Kind == ShardKeyText {
		return json.Marshal(k.Text)
	}
	if k.Kind == ShardKeyNumber {
		return []byte(strconv.FormatUint(k.Number, 10)), nil
	}
	return []byte("null"), nil
}

// shardKeyFromValue accepts a string, a non-negative integer that fits 64 bits, or null for automatic.
func shardKeyFromValue(value any) (PointShardKey, bool) {
	switch typed := value.(type) {
	case nil:
		return PointShardKey{}, true
	case string:
		return TextShardKey(typed), true
	case json.Number:
		digits, err := unsignedDigits(typed)
		if err != nil {
			return PointShardKey{}, false
		}
		number, err := strconv.ParseUint(digits, 10, 64)
		if err != nil {
			return PointShardKey{}, false
		}
		return NumberShardKey(number), true
	}
	return PointShardKey{}, false
}

// Point is a record as Qdrant scrolls it. Vector, Payload and ShardKey keep their raw JSON so numbers are never rounded.
type Point struct {
	ID       PointID         `json:"id"`
	Vector   json.RawMessage `json:"vector,omitempty"`
	Payload  json.RawMessage `json:"payload,omitempty"`
	ShardKey json.RawMessage `json:"shard_key,omitempty"`
}

func (p *Point) UnmarshalJSON(data []byte) error {
	type plain Point
	decoded := plain{}
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	*p = Point(decoded)
	p.Vector = presentRaw(p.Vector)
	p.Payload = presentRaw(p.Payload)
	p.ShardKey = presentRaw(p.ShardKey)
	return nil
}

func presentRaw(raw json.RawMessage) json.RawMessage {
	if string(bytes.TrimSpace(raw)) == "null" {
		return nil
	}
	return raw
}

// ToPointStruct is the part of a scrolled record an upsert carries.
func (p Point) ToPointStruct() Point {
	return Point{ID: p.ID, Vector: p.Vector, Payload: p.Payload}
}

// Digest is the SHA-256 of the canonical JSON of a point's payload and vectors, named, sparse and multi included.
func (p Point) Digest() (string, error) {
	payload, err := decodeNumbers(p.Payload)
	if err != nil {
		return "", fmt.Errorf("point %s payload: %w", p.ID, err)
	}
	vector, err := decodeNumbers(p.Vector)
	if err != nil {
		return "", fmt.Errorf("point %s vector: %w", p.ID, err)
	}
	if !isTruthy(payload) {
		payload = map[string]any{}
	}
	if !isTruthy(vector) {
		vector = nil
	}
	canonical := &strings.Builder{}
	if err := writeCanonical(canonical, map[string]any{"payload": payload, "vector": vector}); err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(canonical.String()))
	return hex.EncodeToString(sum[:]), nil
}

// decodeNumbers parses JSON keeping numbers as json.Number; empty input is null.
func decodeNumbers(raw []byte) (any, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, nil
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("unexpected data after JSON value")
	}
	return value, nil
}

func isTruthy(value any) bool {
	switch typed := value.(type) {
	case nil:
		return false
	case bool:
		return typed
	case string:
		return typed != ""
	case json.Number:
		parsed, err := strconv.ParseFloat(typed.String(), 64)
		return err != nil || parsed != 0
	case map[string]any:
		return len(typed) > 0
	case []any:
		return len(typed) > 0
	}
	return true
}

// writeCanonical renders what Python's json.dumps(sort_keys=True, separators=(",", ":")) renders.
func writeCanonical(out *strings.Builder, value any) error {
	switch typed := value.(type) {
	case nil:
		out.WriteString("null")
	case bool:
		out.WriteString(strconv.FormatBool(typed))
	case string:
		writePythonString(out, typed)
	case json.Number:
		text, err := pythonNumber(typed)
		if err != nil {
			return err
		}
		out.WriteString(text)
	case []any:
		out.WriteByte('[')
		for index, item := range typed {
			if index > 0 {
				out.WriteByte(',')
			}
			if err := writeCanonical(out, item); err != nil {
				return err
			}
		}
		out.WriteByte(']')
	case map[string]any:
		out.WriteByte('{')
		for index, key := range slices.Sorted(maps.Keys(typed)) {
			if index > 0 {
				out.WriteByte(',')
			}
			writePythonString(out, key)
			out.WriteByte(':')
			if err := writeCanonical(out, typed[key]); err != nil {
				return err
			}
		}
		out.WriteByte('}')
	default:
		return fmt.Errorf("cannot canonicalize %T", value)
	}
	return nil
}

func writePythonString(out *strings.Builder, text string) {
	out.WriteByte('"')
	for _, character := range text {
		switch {
		case character == '"':
			out.WriteString(`\"`)
		case character == '\\':
			out.WriteString(`\\`)
		case character == '\n':
			out.WriteString(`\n`)
		case character == '\r':
			out.WriteString(`\r`)
		case character == '\t':
			out.WriteString(`\t`)
		case character == '\b':
			out.WriteString(`\b`)
		case character == '\f':
			out.WriteString(`\f`)
		case character >= ' ' && character <= '~':
			out.WriteRune(character)
		case character > 0xFFFF:
			high, low := utf16.EncodeRune(character)
			fmt.Fprintf(out, `\u%04x\u%04x`, high, low)
		default:
			fmt.Fprintf(out, `\u%04x`, character)
		}
	}
	out.WriteByte('"')
}

// pythonNumber keeps integers exact and renders floats the way Python's repr does.
func pythonNumber(number json.Number) (string, error) {
	text := number.String()
	if !strings.ContainsAny(text, ".eE") {
		parsed, ok := new(big.Int).SetString(text, 10)
		if !ok {
			return "", fmt.Errorf("invalid number %q", text)
		}
		return parsed.String(), nil
	}
	parsed, err := strconv.ParseFloat(text, 64)
	if err != nil && !errors.Is(err, strconv.ErrRange) {
		return "", fmt.Errorf("invalid number %q: %w", text, err)
	}
	return pythonFloat(parsed)
}

func pythonFloat(value float64) (string, error) {
	if value > 1.7976931348623157e308 {
		return "Infinity", nil
	}
	if value < -1.7976931348623157e308 {
		return "-Infinity", nil
	}
	shortest := strconv.FormatFloat(value, 'e', -1, 64)
	sign := ""
	if strings.HasPrefix(shortest, "-") {
		sign = "-"
		shortest = shortest[1:]
	}
	mantissa, exponentText, _ := strings.Cut(shortest, "e")
	exponent, err := strconv.Atoi(exponentText)
	if err != nil {
		return "", fmt.Errorf("format float: %w", err)
	}
	digits := strings.Replace(mantissa, ".", "", 1)
	if exponent < -4 || exponent >= 16 {
		scientific := digits[:1]
		if len(digits) > 1 {
			scientific += "." + digits[1:]
		}
		exponentSign := "+"
		if exponent < 0 {
			exponentSign = "-"
			exponent = -exponent
		}
		return fmt.Sprintf("%s%se%s%02d", sign, scientific, exponentSign, exponent), nil
	}
	if exponent < 0 {
		return sign + "0." + strings.Repeat("0", -exponent-1) + digits, nil
	}
	if len(digits) <= exponent+1 {
		return sign + digits + strings.Repeat("0", exponent+1-len(digits)) + ".0", nil
	}
	return sign + digits[:exponent+1] + "." + digits[exponent+1:], nil
}

// PlanSyncWindow returns the source points missing or different on the target and the target IDs the source lacks, in scroll order.
func PlanSyncWindow(source []Point, target []Point) ([]Point, []PointID, error) {
	onTarget := make(map[PointID]Point, len(target))
	for _, point := range target {
		onTarget[point.ID] = point
	}
	onSource := make(map[PointID]bool, len(source))
	changed := []Point{}
	for _, point := range source {
		onSource[point.ID] = true
		counterpart, present := onTarget[point.ID]
		if !present {
			changed = append(changed, point)
			continue
		}
		sourceDigest, err := point.Digest()
		if err != nil {
			return nil, nil, err
		}
		targetDigest, err := counterpart.Digest()
		if err != nil {
			return nil, nil, err
		}
		if sourceDigest != targetDigest {
			changed = append(changed, point)
		}
	}
	stale := []PointID{}
	for id := range onTarget {
		if !onSource[id] {
			stale = append(stale, id)
		}
	}
	slices.SortFunc(stale, ComparePointIDs)
	return changed, stale, nil
}
