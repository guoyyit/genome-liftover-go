// Package chain 解析 UCSC chain 比对格式文件。
package chain

// Header 描述一条比对链的元信息。
// 坐标均为 0 起始半开区间；当 QStrand 为 '-' 时，
// QStart/QEnd 是相对于 query 序列反向互补链的坐标。
type Header struct {
	Score   float64
	TName   string
	TSize   int
	TStrand byte
	TStart  int
	TEnd    int
	QName   string
	QSize   int
	QStrand byte
	QStart  int
	QEnd    int
	ID      int
}

// Block 是链内一个无缺口的比对块，坐标已展开为绝对坐标。
type Block struct {
	TStart int
	TEnd   int
	QStart int
	QEnd   int
	// DT、DQ 为本块之后、下一块之前在 target/query 上的缺口长度；
	// 最后一块恒为 0。
	DT int
	DQ int
}

// Chain 是一条完整的比对链。
type Chain struct {
	Header
	Blocks []Block
}
