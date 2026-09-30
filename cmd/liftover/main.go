package main

import (
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/guoyyit/genome-liftover-go/batch"
	"github.com/guoyyit/genome-liftover-go/chain"
	"github.com/guoyyit/genome-liftover-go/index"
	"github.com/guoyyit/genome-liftover-go/liftover"
)

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	if len(args) == 0 {
		usage(os.Stderr)
		return 2
	}
	switch args[0] {
	case "lift":
		return cmdLift(args[1:])
	case "batch":
		return cmdBatch(args[1:])
	case "index":
		return cmdIndex(args[1:])
	case "-h", "--help", "help":
		usage(os.Stdout)
		return 0
	default:
		fmt.Fprintf(os.Stderr, "unknown subcommand %q\n\n", args[0])
		usage(os.Stderr)
		return 2
	}
}

func usage(w io.Writer) {
	fmt.Fprint(w, `liftover - local reference-genome coordinate mapping

usage:
  liftover lift  --chain FILE --chrom C --start N --end N [--reverse] [--all-candidates] [--index DIR]
  liftover batch --chain FILE --in intervals.tsv [--out out.tsv] [--err errors.tsv]
                 [--reverse] [--workers N] [--all-candidates] [--index DIR]
  liftover index build --chain FILE --out DIR

Coordinates are 0-based half-open. Chain files may be gzip compressed.
`)
}

func loadIndexOrChain(indexDir, chainFile string) (*index.IntervalIndex, error) {
	if indexDir != "" {
		return index.Load(indexDir)
	}
	if chainFile == "" {
		return nil, fmt.Errorf("either --chain or --index is required")
	}
	chains, err := chain.ParseFile(chainFile)
	if err != nil {
		return nil, err
	}
	return index.New(chains), nil
}

func cmdLift(args []string) int {
	fs := flag.NewFlagSet("lift", flag.ContinueOnError)
	chainPath := fs.String("chain", "", "chain file (plain or .gz)")
	indexDir := fs.String("index", "", "load a persisted index instead of parsing")
	chrom := fs.String("chrom", "", "source chromosome")
	start := fs.Int("start", 0, "interval start (0-based, inclusive)")
	end := fs.Int("end", 0, "interval end (exclusive)")
	reverse := fs.Bool("reverse", false, "map query -> target instead of target -> query")
	allCand := fs.Bool("all-candidates", false, "emit every overlapping candidate")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	idx, err := loadIndexOrChain(*indexDir, *chainPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	dir := liftover.TargetToQuery
	if *reverse {
		dir = liftover.QueryToTarget
	}
	res, err := liftover.NewMapper(idx).Map(*chrom, *start, *end, dir)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if len(res.Candidates) == 0 {
		fmt.Println("status=unmapped")
		return 1
	}
	candidates := res.Candidates
	if !*allCand {
		candidates = candidates[:1]
	}
	for ci, cand := range candidates {
		for _, s := range cand.Segments {
			if s.Status == liftover.StatusMapped {
				fmt.Printf("candidate=%d\tscore=%d\tmapped\t%s:%d-%d\t%c\t%s\t%d\t%d\n",
					cand.ChainID, cand.Score, *chrom, s.SourceStart, s.SourceEnd,
					byte(s.Strand), s.DestChrom, s.DestStart, s.DestEnd)
			} else {
				fmt.Printf("candidate=%d\tscore=%d\tunmapped\t%s:%d-%d\n",
					cand.ChainID, cand.Score, *chrom, s.SourceStart, s.SourceEnd)
			}
		}
		if ci < len(candidates)-1 {
			fmt.Println("--")
		}
	}
	if !res.FullyMapped() {
		return 1
	}
	return 0
}

func cmdBatch(args []string) int {
	fs := flag.NewFlagSet("batch", flag.ContinueOnError)
	chainPath := fs.String("chain", "", "chain file (plain or .gz)")
	indexDir := fs.String("index", "", "load a persisted index instead of parsing")
	inPath := fs.String("in", "", "input TSV: chrom<TAB>start<TAB>end[<TAB>name]")
	outPath := fs.String("out", "", "output TSV (default stdout)")
	errPath := fs.String("err", "", "bad-record TSV (default stderr)")
	reverse := fs.Bool("reverse", false, "map query -> target")
	workers := fs.Int("workers", 1, "concurrent mapping workers")
	allCand := fs.Bool("all-candidates", false, "emit all candidates per record")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *inPath == "" {
		fmt.Fprintln(os.Stderr, "--in is required")
		return 2
	}
	idx, err := loadIndexOrChain(*indexDir, *chainPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	in, err := os.Open(*inPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer in.Close()

	var outFile, errFile *os.File
	out := io.Writer(os.Stdout)
	errOut := io.Writer(os.Stderr)
	if *outPath != "" {
		outFile, err = os.Create(*outPath)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		defer outFile.Close()
		out = outFile
	}
	if *errPath != "" {
		errFile, err = os.Create(*errPath)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		defer errFile.Close()
		errOut = errFile
	}

	dir := liftover.TargetToQuery
	if *reverse {
		dir = liftover.QueryToTarget
	}
	stats, err := batch.Run(liftover.NewMapper(idx), in, out, errOut, batch.Options{
		Direction:            dir,
		Workers:              *workers,
		IncludeAllCandidates: *allCand,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	fmt.Fprintf(os.Stderr,
		"total=%d mapped=%d partial=%d unmapped=%d record_errors=%d\n",
		stats.Total, stats.Mapped, stats.Partial, stats.Unmapped, stats.RecordErrs)
	if stats.Mapped == 0 && stats.Partial == 0 {
		return 1
	}
	return 0
}

func cmdIndex(args []string) int {
	if len(args) == 0 || args[0] != "build" {
		fmt.Fprintln(os.Stderr, "usage: liftover index build --chain FILE --out DIR")
		return 2
	}
	fs := flag.NewFlagSet("index build", flag.ContinueOnError)
	chainPath := fs.String("chain", "", "chain file (plain or .gz)")
	outDir := fs.String("out", "", "output index directory")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	if *chainPath == "" || *outDir == "" {
		fmt.Fprintln(os.Stderr, "--chain and --out are required")
		return 2
	}
	chains, err := chain.ParseFile(*chainPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	idx := index.New(chains)
	if err := idx.Save(*outDir); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	fmt.Fprintf(os.Stderr, "indexed %d chains -> %s\n", len(chains), *outDir)
	return 0
}
