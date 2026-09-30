# genome-liftover-go

本地可运行的参考基因组跨版本区间坐标映射服务。输入为 UCSC chain 格式
（支持 gzip 压缩）的比对文件，输出任意区间在另一参考版本上的坐标映射结果。
整个流程在本地完成，不依赖任何外部服务。

## 构建与测试

```sh
go build ./...
go test ./...          # 单元测试
go test ./... -race    # 含并发竞态检测
```

## 命令行用法

```sh
# 从 chain 文件（可为 .gz）构建索引并持久化
liftover build -chain hg19ToHg38.over.chain.gz -index hg19ToHg38.gli

# 批量映射（直接使用 chain 文件，或加载已持久化的索引）
liftover map -chain hg19ToHg38.over.chain.gz -in regions.tsv -out mapped.tsv
liftover map -index hg19ToHg38.gli -in regions.tsv -out mapped.tsv -workers 8

# 反向映射（query 版本 -> target 版本）
liftover map -index hg19ToHg38.gli -reverse -in regions.tsv
```

输入 TSV 每行一条记录：`chrom<TAB>start<TAB>end[TAB>name...]`，空行与
`#` 注释行被忽略，第 4 列起之后的列被忽略。输出为带表头的 TSV，每行一个
映射片段或未映射区间，列含义见表头；`status` 取值
`mapped` / `partial` / `unmapped` / `error`。统计信息输出到 stderr。

## 坐标约定

- 所有区间均为 **0 起始半开区间** `[start, end)`，与 chain 格式及 BED 一致。
- chain 文件中 `qStrand` 为 `-` 时，`qStart/qEnd` 是相对于 query 序列反向
  互补链的坐标；本工具在解析后统一翻转为正链坐标，输出中的目标区间始终是
  目标染色体的正链坐标，片段的比对方向由 `strand` 列（`+`/`-`）表达。
- chain 头部 `tStrand` 只支持 `+`（UCSC 实际发布的 chain 文件均如此）；
  遇到 `-` 会以 `bad-header` 错误显式拒绝，不做隐式猜测。

## 映射语义

- **方向**：chain 本身是有向的（target -> query）。正向映射（Forward）把
  target 版本坐标映射到 query 版本；反向映射（Reverse）沿同一条链把 query
  版本坐标映射回 target 版本。两个方向使用同一索引。
- **缺口拆分**：chain 块之间的缺口（`dt`/`dq`）不对齐。跨越缺口的查询区间
  被拆分为多个映射片段，片段按源坐标升序输出，各自保留方向（`strand`）。
- **未映射区域**：查询区间中不被任何链覆盖的部分（包括链内缺口）作为
  `unmapped` 片段显式输出，绝不静默丢弃。记录级状态：
  全部覆盖 `mapped`，部分覆盖 `partial`，完全未覆盖 `unmapped`。
- **歧义处理**：多条链重叠覆盖同一源区域时，按确定性规则决胜——
  对齐分数高者优先，分数相同则链 ID 小者优先。分数相同的其他链 ID 记录
  在输出 `tied` 列中作为并列候选，供下游自行裁决。
- **边界规则**：区间与链边界恰好相接（`end == start`）不算重叠；查询区间
  超出链覆盖范围的部分按未映射处理；`start >= end`、负坐标、缺列的输入
  记录记为 `error` 行，不影响同批其他记录。

## 往返映射

同一区间经同一比对链正向再反向映射后恢复为原区间（链内缺口覆盖不到的
部分除外——它们本来就不会出现在正向结果中）。测试中对链覆盖区做了系统
扫描验证该性质（`TestRoundTripSweep`）。

**歧义链造成的往返差异**：当多条链以相同最高分并列覆盖同一区域时，正向
映射按决胜规则选中链 ID 最小者；把结果反向映射时，如果目标区域同样存在
多条并列链，反向映射也独立适用同一决胜规则，可能选中另一条链，从而落在
源版本的不同坐标上。并列候选始终记录在 `tied` 列，需要严格往返的场景应
检查该列并对并列链逐一验证。

## 输入校验

chain 解析对损坏输入给出可区分的错误类别（`chain.ErrKind`），任何非法
内容都会报错，绝不静默跳过：

- `syntax`：行格式非法（字段数、关键字、数字解析失败、链外出现块数据等）
- `truncated`：输入在一条链中途结束
- `bad-header`：头部取值非法（链方向符号非法、坐标越界、tStrand 为 `-` 等）
- `bad-block`：块数据与头部声明不一致（块越界、总长度不符、块大小为 0 等）

gzip 压缩按文件魔数（`1f 8b`）自动识别，与文件扩展名无关。

## 批量处理、并发与内存

- 批量映射流式读取输入，在途记录数有界（与并发度成正比），内存占用不随
  输入总量增长。
- 输出顺序固定为输入顺序，与并发度无关：同一输入在 `-workers 1` 与任意
  并发度下产生逐字节一致的结果（有测试保证）。
- 单条记录的解析错误以 `error` 行输出并计入统计，不中断整批处理。
- 索引构建后不可变，可安全地被多个 goroutine 并发查询；构建中的新索引与
  查询中的旧索引互不影响，不存在撕裂状态（`-race` 测试覆盖）。

## 索引持久化

`liftover build` 把索引写入带魔数与版本标识的文件。写入先落到同目录临时
文件，fsync 后原子改名，写入中途失败不会损坏目标路径上已有的索引。加载
时校验魔数并重建内存索引，重载后的索引与新建索引产生逐条一致的结果
（`TestSaveLoadRoundTrip` 覆盖）。

## 代码结构

- `chain/`：chain 格式流式解析器与错误分类
- `liftover/`：区间索引、映射逻辑、索引持久化
- `batch/`：TSV 批量映射（保序、有界并发、统计）
- `cmd/liftover/`：命令行入口
