# genome-liftover-go

本地可运行的参考基因组跨版本区间坐标映射服务。输入为 UCSC
[chain](https://genome.ucsc.edu/goldenPath/help/chain.html) 格式比对文件
（可 gzip 压缩），输出任意 0 起始半开区间在另一参考版本上的映射结果。
全程离线，无外部服务依赖。

## 构建

```sh
go build ./...
go test ./...
```

## 坐标约定

- 所有输入/输出坐标均为 **0 起始、半开区间 `[start,end)`**（与 BED 一致）。
- chain header 的 `tName/tSize/tStrand/tStart/tEnd` 与
  `qName/qSize/qStrand/qStart/qEnd` 语义同 UCSC；目标链方向必须为 `+`，
  查询链方向为 `+` 或 `-`。
- 输出中的 `Strand` 表示该片段相对查询链的方向：正向链为 `+`，反向链为 `-`。
  反向链上映射时片段顺序仍按**源坐标升序**排列。

## 映射语义

- 支持两个方向：`target -> query`（默认）与 `query -> target`（`--reverse`）。
- 区间与某条链的对齐块求交。源侧落入 gap（target->query 时的 target gap，
  或 query->target 时的 query gap）的位置无法对齐，输出为独立的 `unmapped`
  片段，绝不静默丢弃。
- 仅存在于目的侧的 gap（`dt=0,dq>0` 或反之）不拆分源区间：源侧相邻的对齐块
  合并为一个 mapped 片段。例外：`target -> query` 且 query 为反向链时，相邻块
  在目的坐标上会交叉，因此仍逐块输出以便下游使用。
- 反向链（`qStrand=-`）按
  `gPos = qSize - qChainPos - len` 做翻转，保证坐标与方向一致。
- 查询区间超出链覆盖范围时，超出部分同样作为 `unmapped` 片段显式报告；
  完全没有重叠链时返回空候选（`status=unmapped`）。

## 歧义处理

多条链与同一区间重叠时，每条链产生一个候选（`Candidate`）：

1. **分数优先**：chain header 的 `score` 高者胜；
2. **ID 决胜**：分数相同时取较小的 chain ID，保证结果确定。

`Result.BestCandidate()` 返回上述规则选出的候选；CLI 加 `--all-candidates`、
批量接口设置 `IncludeAllCandidates` 可并列输出全部候选及各自分数、链 ID。

## 往返与歧义说明

在**同一条 chain、完全落在对齐块内**的区间上，`target->query` 后再
`query->target` 会恢复原区间（反向链同样成立，见 `TestRoundTrip`）。

以下情况往返可能不恢复原区间，这是 chain 数据本身的歧义，非实现缺陷：

- 区间穿过 gap：部分位置在链上无对齐，往返只能恢复 mapped 子段；
- 多链重叠区域：正向选择的最高分链与反向索引到的最高分链可能不是同一条
  （两个方向的 `score` 决胜独立进行），此时应使用 `--all-candidates` 人工裁决；
- 反向链上相邻对齐块若被拆成多段，各段单独往返可恢复，合并段需按片段处理。

## 批量处理

TSV 输入每行：`chrom<TAB>start<TAB>end[<TAB>name]`，空行与 `#` 开头行忽略。

- 输出行顺序与输入记录顺序严格一致，与 `--workers` 无关（worker 并发计算，
  输出按输入序号重排）；单条记录解析或映射失败只写入错误流，不中断整批。
- 每条记录按选中候选分类为 `mapped` / `partial` / `unmapped`，结束时输出
  计数 `total/mapped/partial/unmapped/record_errors`。
- 索引在构建后不可变，多 worker 并发查询同一索引无锁、无撕裂状态。

## 大输入与内存

- 解析与批量处理均为流式（bufio.Scanner，64KiB 初始缓冲，单行上限 16/64MiB），
  不把输入文件整体载入内存。
- 每条染色体维护按 start（再按 chain ID）排序的链数组。区间查询分两步：
  1. 二分定位最后一个 `start < end` 的位置（start 单调，上界精确）；
  2. 在该前缀上用 **max-end 线段树** 逐个找出 `end > start` 的链。
  这样在大量链相互重叠、end **不单调** 时也能返回全部真实重叠的链，
  既不漏报也不误报（端点相接 `end == start` 不算重叠）。
  复杂度约 O(log n + k log n)，k 为真实重叠链数；稀疏查询基本是 O(log n)，
  **查询耗时不随该染色体链总数线性增长**。线段树为 O(n) 空间，索引内存与
  链数量同阶，构建为 O(n log n)。
- query 侧索引保存的是**基因组坐标**区间：反向链（`qStrand=-`）的
  `qStart/qEnd` 为链坐标，构建索引时按 `qSize` 翻转为
  `[qSize-qEnd, qSize-qStart)`，因此反向查询可直接使用基因组坐标。
- 索引本身与链数量同阶；chain 是不可变共享对象，不会被批量 worker 复制。

### 密集重叠正确性说明（修复点）

旧实现在按 start 排序的数组上直接对 ends 做二分定位下界。当同一染色体上
存在数千条相互重叠的链时，ends 并不单调：一条排在前面的长链（end 很大）
会让二分把本应命中的更早长链跳过（**漏报、误判未映射**），而后续线性扫描
又不复查 end，会把仅 start 落在区间内的短链误纳（**误报**）。正向/反向、
target/query 两个方向都受影响。修复后候选集合与「逐条精确比较」的参考实现
在数千条密集重叠链夹具上完全一致，覆盖链首端、末端、恰好相接、跨越边界等
端点情形（见 `index/dense_test.go`、`liftover/dense_mapper_test.go`）。


## 索引持久化

```sh
liftover index build --chain hg19ToHg38.chain.gz --out ./idx
liftover lift  --index ./idx --chrom chr1 --start 100 --end 200
liftover batch --index ./idx --in in.tsv --workers 8
```

- 格式：自定义带魔数 `LIFTIDX1` 与版本号的二进制，末尾附 SHA-256 校验和；
  载入时校验魔数、版本、载荷完整性并重新 `Validate` 每条链。
- 写入采用「同目录临时文件 + fsync + 原子 rename + 目录 fsync」，
  写入中途失败只留下/清理临时文件，已有 `index.bin` 不会被截断或损坏。
- 重载后的索引与新建索引对相同查询产生逐条一致结果（候选集合、mapped/
  unmapped 片段、分类计数），见 `TestSaveLoadRoundTrip` 与密集夹具上的
  `TestDensePersistReloadEquivalence`、`TestDenseReloadConcurrentConsistency`。

### 索引兼容性

- 当前持久化格式仍为魔数 `LIFTIDX1`、版本 1，**格式未变化**：已经落盘的
  旧索引无需重建即可直接加载，且查询结果与重建后完全一致（线段树在加载时
  从链数组重新构建，不写入文件）。
- 若未来格式/版本不被支持，载入会返回可区分的错误
  `unsupported index version …`（魔数不符返回 `bad magic`，校验失败返回
  `checksum mismatch`），绝不静默产生错误结果；此时用
  `liftover index build --chain … --out …` 重新构建即可恢复。
- 写入使用同目录临时文件 + fsync + 原子 rename + 目录 fsync；写入被中断时
  已有的 `index.bin` 保持可加载且字节不变（见 `TestSaveFailureKeepsOldIndex`）。

## 验证方法（本地可重复执行）

全部离线、无外部服务/网络/凭据：

```sh
# 构建与完整测试
go build ./...
go test ./...

# 竞态检测（并发批量与共享只读索引）
go test -race ./...

# 密集重叠专项（数千条相互重叠链、正反链、两个方向、端点/边界、
# 跨 gap、多链歧义、往返、持久化重载、批量并发）
go test ./... -run 'Dense|Minus|RoundTrip|Ambiguous' -v

# 查询/构建的复杂度与内存随链数量的变化
go test ./index/ -bench . -benchmem
```

测试夹具均在本地确定性生成（固定随机种子），并与逐条精确比较的参考实现
交叉验证；稀疏查询在 1k→16k 链时耗时基本只随 log n 增长。

## 输入校验

解析失败统一返回 `chain.ParseError`，并带行号与三类可区分原因：

- `malformed`：字段数/整数/strand/区间方向等格式非法；
- `truncated`：chain 缺少终止行、记录或 gzip 流被截断；
- `corrupt`：block 无法重建 header 声明的起止、坐标越过染色体长度、
  gap 非法、校验和不符等语义/数据损坏。

## 快速示例

```sh
# 单条区间
liftover lift --chain example.chain --chrom chr1 --start 90 --end 250

# 批量，4 worker，错误单独成文件
liftover batch --chain example.chain.gz --in in.tsv \
  --out out.tsv --err bad.tsv --workers 4
```

退出码：`0` 成功（lift 要求区间完全 mapped）；`1` 存在未映射/部分映射或
数据错误；`2` 命令行用法错误。
