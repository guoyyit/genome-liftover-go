package chain

// Strand represents a chain strand direction. Target strand is always '+'.
type Strand byte

const (
	StrandPlus  Strand = '+'
	StrandMinus Strand = '-'
)

// AlignBlock is one ungapped aligned segment of a chain, expressed in
// target coordinates with length Size.
type AlignBlock struct {
	TStart int
	QStart int
	Size   int
}

// Chain models one chain alignment record between two reference versions.
type Chain struct {
	Score        int64
	TargetName   string
	TargetSize   int
	TargetStrand Strand
	TargetStart  int
	TargetEnd    int
	QueryName    string
	QuerySize    int
	QueryStrand  Strand
	QueryStart   int
	QueryEnd     int
	ID           int64
	Blocks       []AlignBlock
}
