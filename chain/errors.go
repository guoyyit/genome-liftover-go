package chain

import "fmt"

// ErrKind 区分解析失败的原因，便于调用方分类处理。
type ErrKind int

const (
	// ErrSyntax 表示行格式非法（字段数、关键字、数字解析失败等）。
	ErrSyntax ErrKind = iota
	// ErrTruncated 表示输入在一条链中途结束。
	ErrTruncated
	// ErrBadHeader 表示 chain 头行字段取值非法。
	ErrBadHeader
	// ErrBadBlock 表示块数据与头声明不一致（越界、重叠、总长度不符等）。
	ErrBadBlock
)

func (k ErrKind) String() string {
	switch k {
	case ErrSyntax:
		return "syntax"
	case ErrTruncated:
		return "truncated"
	case ErrBadHeader:
		return "bad-header"
	case ErrBadBlock:
		return "bad-block"
	default:
		return "unknown"
	}
}

// ParseError 是带行号与分类的解析错误。
type ParseError struct {
	Kind ErrKind
	Line int
	Msg  string
}

func (e *ParseError) Error() string {
	return fmt.Sprintf("chain parse error (%s) at line %d: %s", e.Kind, e.Line, e.Msg)
}
