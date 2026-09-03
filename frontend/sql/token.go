package sql

// TokenKind identifies one bounded SQL lexical token.
type TokenKind uint8

const (
	TokenInvalid TokenKind = iota
	TokenEOF
	TokenIdentifier
	TokenString
	TokenInteger
	TokenParameter
	TokenTrue
	TokenFalse
	TokenNull
	TokenAnd
	TokenOr
	TokenNot
	TokenIn
	TokenIs
	TokenAs
	TokenFor
	TokenTo
	TokenUsing
	TokenWith
	TokenCheck
	TokenCreate
	TokenPolicy
	TokenOn
	TokenPermissive
	TokenRestrictive
	TokenAll
	TokenSelect
	TokenInsert
	TokenUpdate
	TokenDelete
	TokenPublic
	TokenEqual
	TokenNotEqual
	TokenLess
	TokenLessEqual
	TokenGreater
	TokenGreaterEqual
	TokenLParen
	TokenRParen
	TokenComma
	TokenSemicolon
	TokenDot
	TokenMinus
)

// Tokens owns source and parallel lexical columns. Integer values are zero for
// non-integer rows.
type Tokens struct {
	Source   []byte
	Kinds    []TokenKind
	Starts   []uint32
	Ends     []uint32
	Integers []int64
}
