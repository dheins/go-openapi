package loader

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"strconv"

	"go.yaml.in/yaml/v3"
)

// maxJSONValues bounds how many values converting one YAML document to
// JSON may produce. Aliases can make a tiny document expand exponentially
// ("billion laughs"); no real OpenAPI document comes near this.
const maxJSONValues = 5_000_000

// writeNode writes n as JSON. Walking the node tree, rather than decoding
// into map[string]any, preserves mapping key order.
func writeNode(buf *bytes.Buffer, n *yaml.Node) error {
	budget := maxJSONValues
	return writeNodeLimited(buf, n, &budget)
}

func writeNodeLimited(buf *bytes.Buffer, n *yaml.Node, budget *int) error {
	if *budget--; *budget < 0 {
		return fmt.Errorf("line %d: the document expands to more than %d values (YAML aliases?)", n.Line, maxJSONValues)
	}
	switch n.Kind {
	case yaml.DocumentNode:
		return writeNodeLimited(buf, n.Content[0], budget)
	case yaml.AliasNode:
		return writeNodeLimited(buf, n.Alias, budget)
	case yaml.MappingNode:
		buf.WriteByte('{')
		for i := 0; i < len(n.Content); i += 2 {
			key, value := n.Content[i], n.Content[i+1]
			if key.Kind != yaml.ScalarNode {
				return fmt.Errorf("line %d: mapping keys must be scalars", key.Line)
			}
			if key.Tag == "!!merge" {
				return fmt.Errorf("line %d: YAML merge keys are not supported", key.Line)
			}
			if i > 0 {
				buf.WriteByte(',')
			}
			if err := writeString(buf, key.Value); err != nil {
				return err
			}
			buf.WriteByte(':')
			if err := writeNodeLimited(buf, value, budget); err != nil {
				return err
			}
		}
		buf.WriteByte('}')
		return nil
	case yaml.SequenceNode:
		buf.WriteByte('[')
		for i, item := range n.Content {
			if i > 0 {
				buf.WriteByte(',')
			}
			if err := writeNodeLimited(buf, item, budget); err != nil {
				return err
			}
		}
		buf.WriteByte(']')
		return nil
	case yaml.ScalarNode:
		return writeScalar(buf, n)
	default:
		return fmt.Errorf("line %d: unexpected YAML node kind %d", n.Line, n.Kind)
	}
}

func writeScalar(buf *bytes.Buffer, n *yaml.Node) error {
	switch n.ShortTag() {
	case "!!null":
		buf.WriteString("null")
	case "!!bool":
		var b bool
		if err := n.Decode(&b); err != nil {
			return fmt.Errorf("line %d: %w", n.Line, err)
		}
		buf.WriteString(strconv.FormatBool(b))
	case "!!int":
		// Decoding normalises hex, octal and underscores to a plain integer.
		var i int64
		if err := n.Decode(&i); err == nil {
			buf.WriteString(strconv.FormatInt(i, 10))
			break
		}
		var u uint64
		if err := n.Decode(&u); err != nil {
			return fmt.Errorf("line %d: %w", n.Line, err)
		}
		buf.WriteString(strconv.FormatUint(u, 10))
	case "!!float":
		var f float64
		if err := n.Decode(&f); err != nil {
			return fmt.Errorf("line %d: %w", n.Line, err)
		}
		if math.IsInf(f, 0) || math.IsNaN(f) {
			return fmt.Errorf("line %d: %s has no JSON representation", n.Line, n.Value)
		}
		buf.WriteString(strconv.FormatFloat(f, 'g', -1, 64))
	default:
		return writeString(buf, n.Value)
	}
	return nil
}

func writeString(buf *bytes.Buffer, s string) error {
	b, err := json.Marshal(s)
	if err != nil {
		return err
	}
	buf.Write(b)
	return nil
}
