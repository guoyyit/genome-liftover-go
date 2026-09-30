// liftover 是本地参考基因组跨版本区间坐标映射命令行工具。
package main

import (
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/guoyyit/genome-liftover-go/batch"
	"github.com/guoyyit/genome-liftover-go/chain"
	"github.com/guoyyit/genome-liftover-go/liftover"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

const usage = `liftover - 参考基因组跨版本区间坐标映射

用法:
  liftover build -chain FILE[.gz] -index FILE
      从 chain 文件构建索引并持久化
  liftover map (-chain FILE[.gz] | -index FILE) [选项]
      批量映射 TSV 区间（默认从标准输入读取、写到标准输出）

map 选项:
  -in FILE       输入 TSV（chrom<TAB>start<TAB>end[TAB>name...]），默认 stdin
  -out FILE      输出 TSV，默认 stdout
  -reverse       反向映射（query -> target）
  -workers N     并发度，默认 4；结果与并发度无关
`

func run() error {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		return fmt.Errorf("missing subcommand")
	}
	switch os.Args[1] {
	case "build":
		return cmdBuild(os.Args[2:])
	case "map":
		return cmdMap(os.Args[2:])
	default:
		fmt.Fprint(os.Stderr, usage)
		return fmt.Errorf("unknown subcommand %q", os.Args[1])
	}
}

func loadChains(path string) (*liftover.Index, error) {
	rc, err := chain.Open(path)
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	b := liftover.NewBuilder()
	n := 0
	err = chain.Parse(rc, func(c *chain.Chain) error {
		n++
		return b.Add(c)
	})
	if err != nil {
		return nil, err
	}
	fmt.Fprintf(os.Stderr, "loaded %d chains from %s\n", n, path)
	return b.Build(), nil
}

func cmdBuild(args []string) error {
	fs := flag.NewFlagSet("build", flag.ContinueOnError)
	chainPath := fs.String("chain", "", "chain 文件路径（可为 .gz）")
	indexPath := fs.String("index", "", "索引输出路径")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *chainPath == "" || *indexPath == "" {
		return fmt.Errorf("build 需要 -chain 与 -index 参数")
	}
	ix, err := loadChains(*chainPath)
	if err != nil {
		return err
	}
	if err := ix.Save(*indexPath); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "index written to %s\n", *indexPath)
	return nil
}

func cmdMap(args []string) error {
	fs := flag.NewFlagSet("map", flag.ContinueOnError)
	chainPath := fs.String("chain", "", "chain 文件路径（可为 .gz）")
	indexPath := fs.String("index", "", "索引文件路径")
	inPath := fs.String("in", "", "输入 TSV，默认 stdin")
	outPath := fs.String("out", "", "输出 TSV，默认 stdout")
	reverse := fs.Bool("reverse", false, "反向映射（query -> target）")
	workers := fs.Int("workers", 4, "并发度")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if (*chainPath == "") == (*indexPath == "") {
		return fmt.Errorf("map 需要且只能需要 -chain 或 -index 之一")
	}

	var ix *liftover.Index
	var err error
	if *indexPath != "" {
		ix, err = liftover.LoadIndex(*indexPath)
	} else {
		ix, err = loadChains(*chainPath)
	}
	if err != nil {
		return err
	}

	var in io.Reader = os.Stdin
	if *inPath != "" {
		f, err := os.Open(*inPath)
		if err != nil {
			return err
		}
		defer f.Close()
		in = f
	}
	var out io.Writer = os.Stdout
	if *outPath != "" {
		f, err := os.Create(*outPath)
		if err != nil {
			return err
		}
		defer f.Close()
		out = f
	}

	dir := liftover.Forward
	if *reverse {
		dir = liftover.Reverse
	}
	stats, err := batch.Run(in, out, ix, dir, *workers)
	fmt.Fprintf(os.Stderr, "total=%d mapped=%d partial=%d unmapped=%d failed=%d\n",
		stats.Total, stats.Mapped, stats.Partial, stats.Unmapped, stats.Failed)
	return err
}
