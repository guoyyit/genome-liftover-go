// Package liftover 提供基于 chain 比对的区间坐标映射。
package liftover

// Direction 指定映射方向。
type Direction int

const (
	// Forward 从 chain 的 target 版本映射到 query 版本。
	Forward Direction = iota
	// Reverse 从 chain 的 query 版本映射回 target 版本。
	Reverse
)

// Interval 是 0 起始半开区间 [Start, End)。
type Interval struct {
	Name  string
	Start int
	End   int
}

// Segment 是查询区间的一个映射片段。
type Segment struct {
	Target   Interval // 目标版本上的坐标
	Strand   byte     // 片段在目标上的方向：'+' 或 '-'
	ChainID  int      // 产生该片段的链
	Score    float64  // 链的对齐分数
	SrcStart int      // 该片段覆盖查询区间的起点（相对查询坐标系）
	SrcEnd   int      // 该片段覆盖查询区间的终点
	// Tied 记录与本片段并列最优（同分且决胜规则并列）的其他链 ID，
	// 非空表示该片段存在歧义。
	Tied []int
}

// Status 表示一条记录的映射状态。
type Status int

const (
	// StatusMapped 表示查询区间被完整映射。
	StatusMapped Status = iota
	// StatusPartial 表示查询区间只有部分被映射。
	StatusPartial
	// StatusUnmapped 表示查询区间完全未映射。
	StatusUnmapped
)

// Result 是一条查询区间的完整映射结果。
type Result struct {
	Query    Interval
	Segments []Segment  // 按查询坐标升序排列
	Unmapped []Interval // 查询区间中未被任何链覆盖的部分
	Status   Status
}
