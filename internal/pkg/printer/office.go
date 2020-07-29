package printer

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/phayes/freeport"
	"github.com/thecodingmachine/gotenberg/internal/pkg/conf"
	"github.com/thecodingmachine/gotenberg/internal/pkg/xcontext"
	"github.com/thecodingmachine/gotenberg/internal/pkg/xerror"
	"github.com/thecodingmachine/gotenberg/internal/pkg/xexec"
	"github.com/thecodingmachine/gotenberg/internal/pkg/xlog"
	"github.com/thecodingmachine/gotenberg/internal/pkg/xrand"
)

type officePrinter struct {
	logger xlog.Logger
	fpaths []string
	opts   OfficePrinterOptions
}

// OfficePrinterOptions helps customizing the
// Office Printer behaviour.
type OfficePrinterOptions struct {
	WaitTimeout float64
	PaperFormat string
	PaperWidth  int
	PaperHeight int
	Landscape   bool
	PageRanges  string
}

// DefaultOfficePrinterOptions returns the default
// Office Printer options.
func DefaultOfficePrinterOptions(config conf.Config) OfficePrinterOptions {
	return OfficePrinterOptions{
		WaitTimeout: config.DefaultWaitTimeout(),
		PaperFormat: "",
		PaperWidth:  0,
		PaperHeight: 0,
		Landscape:   false,
		PageRanges:  "",
	}
}

// NewOfficePrinter returns a Printer which
// is able to convert Office documents to PDF.
func NewOfficePrinter(logger xlog.Logger, fpaths []string, opts OfficePrinterOptions) Printer {
	return officePrinter{
		logger: logger,
		fpaths: fpaths,
		opts:   opts,
	}
}

func (p officePrinter) Print(destination string) error {
	const op string = "printer.officePrinter.Print"
	logOptions(p.logger, p.opts)
	ctx, cancel := xcontext.WithTimeout(p.logger, p.opts.WaitTimeout)
	defer cancel()
	resolver := func() error {
		// see https://github.com/thecodingmachine/gotenberg/issues/139.
		sort.Strings(p.fpaths)
		fpaths := make([]string, len(p.fpaths))
		dirPath := filepath.Dir(destination)
		for i, fpath := range p.fpaths {
			baseFilename := xrand.Get()
			tmpDest := fmt.Sprintf("%s/%d%s.pdf", dirPath, i, baseFilename)
			p.logger.DebugOpf(op, "converting '%s' to PDF...", fpath)
			if err := p.unoconv(ctx, fpath, tmpDest); err != nil {
				return err
			}
			p.logger.DebugOpf(op, "'%s.pdf' created", baseFilename)
			fpaths[i] = tmpDest
		}
		if len(fpaths) == 1 {
			p.logger.DebugOp(op, "only one PDF created, nothing to merge")
			return os.Rename(fpaths[0], destination)
		}
		m := mergePrinter{
			logger: p.logger,
			ctx:    ctx,
			fpaths: fpaths,
		}
		return m.Print(destination)
	}
	if err := resolver(); err != nil {
		return xcontext.MustHandleError(
			ctx,
			xerror.New(op, err),
		)
	}
	return nil
}

func (p officePrinter) unoconv(ctx context.Context, fpath, destination string) error {
	const op string = "printer.unoconv"
	resolver := func() error {
		dirName := xrand.Get()
		port, err := freeport.GetFreePort()
		if err != nil {
			return err
		}
		args := []string{
			"--user-profile",
			fmt.Sprintf("///tmp/%s", dirName),
			"--port",
			fmt.Sprintf("%d", port),
			"--format",
			"pdf",
		}
		if p.opts.PaperFormat != "" {
			args = append(args, "--printer", fmt.Sprintf("PaperFormat=%s", p.opts.PaperFormat))
		}
		if p.opts.PaperWidth > 0 && p.opts.PaperHeight > 0 {
			args = append(args, "--printer", fmt.Sprintf("PaperSize=%sx%d", p.opts.PaperWidth, p.opts.PaperHeight))
		}
		if p.opts.Landscape {
			args = append(args, "--printer", "PaperOrientation=landscape")
		}
		if p.opts.PageRanges != "" {
			args = append(args, "--export", fmt.Sprintf("PageRange=%s", p.opts.PageRanges))
		}
		args = append(args, "--output", destination, fpath)
		err = xexec.Run(ctx, p.logger, "unoconv", args...)
		// always remove user profile folders created by LibreOffice.
		// see https://github.com/thecodingmachine/gotenberg/issues/192.
		go cleanupUserProfile(p.logger, dirName)
		if err != nil {
			// find a way to check it in the handlers?
			if p.opts.PageRanges != "" && strings.Contains(err.Error(), "exit status 5") {
				return xerror.Invalid(
					op,
					fmt.Sprintf("'%s' is not a valid LibreOffice page ranges", p.opts.PageRanges),
					err,
				)
			}
			return err
		}
		return nil
	}
	if err := resolver(); err != nil {
		return xerror.New(op, err)
	}
	return nil
}

func cleanupUserProfile(logger xlog.Logger, dirName string) {
	const op = "printer.cleanupUserProfile"
	path := fmt.Sprintf("/tmp/%s", dirName)
	if err := os.RemoveAll(path); err != nil {
		// find a way to bubble up this error?
		logger.ErrorOpf(op, "failed to remove user profile directory '%s': %s", path, err.Error())
	}
}

// Compile-time checks to ensure type implements desired interfaces.
var (
	_ = Printer(new(officePrinter))
)
