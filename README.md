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
- 每条染色体维护按 `(start, chain ID)` 排序的链数组，并在其上构建
  **子树最大 end 增强的隐式区间树**（cgranges 式）：查询沿中序遍历，
  用 `maxEnd <= start` 剪枝整棵子树，用 `start >= end` 剪掉右子树。
  复杂度为输出敏感的 O(log n + k)（k 为命中数），命中顺序即排序数组顺序，
  与文档约定一致。同一染色体上数千条链大量重叠时，候选集合与逐条精确
  比较完全一致，不漏报、不误报未映射；查询耗时不随链总数线性增长
  （见 `BenchmarkDenseQueryScaling`）。
- query 侧索引保存的是**基因组坐标**区间：反向链（`qStrand=-`）的
  `qStart/qEnd` 为链坐标，构建索引时按 `qSize` 翻转为
  `[qSize-qEnd, qSize-qStart)`，因此反向查询可直接使用基因组坐标。
- 索引本身与链数量同阶；chain 是不可变共享对象，不会被批量 worker 复制。

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
- 重载后的索引与新建索引对相同查询产生逐条一致结果（见
  `TestSaveLoadRoundTrip` 与密集数据上的 `TestDenseSaveLoadIdentical`）。
- **兼容性**：本次密集重叠修复只改了内存中的查询算法，磁盘格式未变；
  修复前落盘的 `LIFTIDX1` 索引继续可加载且语义一致（由金文件测试
  `TestLoadGoldenV1Index` 锁定）。若未来格式演进，旧版本号会在加载时
  给出可区分错误，重新执行 `liftover index build` 即可恢复。

## 输入校验

解析失败统一返回 `chain.ParseError`，并带行号与三类可区分原因：

- `malformed`：字段数/整数/strand/区间方向等格式非法；
- `truncated`：chain 缺少终止行、记录或 gzip 流被截断；
- `corrupt`：block 无法重建 header 声明的起止、坐标越过染色体长度、
  gap 非法、校验和不符等语义/数据损坏。

## 密集重叠场景的正确性

同一染色体上数千条链大量重叠时，候选检索必须完整：任何与查询区间真实
重叠（0 起始半开，端点相接不算重叠）的链都会作为候选返回，两个查询
方向、正负链一致成立。历史上此处曾在按 start 排序的数组上对 end 做二分
（end 在该顺序下非单调），导致密集数据漏候选、误报未映射；现已改为
maxEnd 增强区间树并由以下自动化测试锁定：

- `index.TestDenseOverlapMatchesBruteForce`：3000 条密集重叠链上，随机
  区间 + 链首/链尾/端点相接/跨链边界查询，与逐条精确比较的参考实现
  （`internal/testutil.Overlap`）逐一比对，两个方向同时验证；
- `liftover.TestDenseCandidateCompleteness`：映射层候选集合与顺序
  （分数降序、ID 升序决胜）同暴力参考一致；
- `liftover.TestDenseRoundTrip`：完全落入对齐块内的区间经同一条链
  往返映射恢复原区间，正负链各覆盖百余条；
- `liftover.TestDenseGapSplit`：跨缺口区间拆分且片段不重不漏；
- `batch.TestDenseBatchConcurrencyEquivalence`：1 与 8 worker 输出逐字节
  一致、顺序与输入一致、统计与逐条映射参考一致；
- `index.TestDenseSaveLoadIdentical`：密集索引重载后命中集合与序列化
  字节完全一致；
- `cmd/liftover.TestDenseCLIEndToEnd`：CLI 下 chain 文件与持久化索引
  的批量输出逐字节一致。

夹具由 `internal/testutil` 确定性生成（固定种子），不依赖每条染色体
只有一两条链的合成数据，也不依赖任何外部文件或服务。

## 验证方法

全部验证均可离线重复执行：

```sh
go build ./...
go vet ./...
go test ./...          # 单元 + 密集夹具交叉验证
go test -race ./...    # 并发一致性（无撕裂状态）

# 查询耗时不随同染色体链数线性增长（重叠深度恒定，曲线应基本平坦）
go test ./index/ -run=NONE -bench=BenchmarkDenseQueryScaling -benchtime=200x
```

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
