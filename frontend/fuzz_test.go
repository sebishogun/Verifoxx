package frontend

import "testing"

func FuzzBuilder(f *testing.F) {
	f.Add([]byte("team == red"), uint8(4), int64(7))
	f.Add([]byte("é"), uint8(1), int64(-1))
	f.Fuzz(func(t *testing.T, source []byte, operations uint8, integer int64) {
		if len(source) > 128 {
			t.Skip()
		}
		limits := DefaultLimits()
		limits.MaxSourceBytes = 128
		limits.MaxNodes = 32
		limits.MaxLiterals = 64
		limits.MaxChildren = 64
		bindings := BindingSet{
			Name: "fuzz", Version: "v1",
			Fields: []Binding{
				{Source: "team", Target: "subject.team", Kind: ValueKindString, Group: FieldGroupSubject},
				{Source: "count", Target: "context.count", Kind: ValueKindInteger, Group: FieldGroupContext},
			},
		}
		builder, err := NewBuilder(source, bindings, limits)
		if err != nil {
			return
		}
		span := Span{End: uint32(len(source))}
		nodes := make([]NodeID, 0, 16)
		count := int(operations%16) + 1
		for i := 0; i < count; i++ {
			var node NodeID
			switch i % 5 {
			case 0:
				node, err = builder.AddBoolean(i&1 == 0, span)
			case 1:
				node, err = builder.AddCompare(2, CompareOpGreaterEqual, IntegerLiteral(integer), span)
			case 2:
				node, err = builder.AddIn(1, []Literal{StringLiteral([]byte("a")), StringLiteral([]byte("b"))}, span)
			case 3:
				node, err = builder.AddDefined(FieldID(i%3), span)
			default:
				if len(nodes) >= 2 {
					node, err = builder.AddAny(nodes[len(nodes)-2:], span)
				} else {
					node, err = builder.AddNot(NodeID(len(nodes)), span)
				}
			}
			if err == nil {
				nodes = append(nodes, node)
			}
		}
		if len(nodes) != 0 {
			_, _ = builder.Finish(nodes[len(nodes)-1], DefaultEscalate)
		}
	})
}
