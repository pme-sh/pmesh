package cmd

import (
	"context"
	"io"
	"log"
	"os"
	"time"

	"get.pme.sh/pmesh/config"
	"get.pme.sh/pmesh/rundown"
	"get.pme.sh/pmesh/xlog"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

func parseFriendlyTime(s string) (t time.Time, err error) {
	if dur, err := time.ParseDuration(s); err == nil {
		return time.Now().Add(-dur), nil
	}
	if t, err = time.Parse("_2/1/2006 15:04 MST", s); err == nil {
		return
	}
	if t, err = time.Parse("_2/1/2006 15:04", s); err == nil {
		return
	}
	return time.Parse(time.RFC3339, s)
}

func tailOptionsP(fs *pflag.FlagSet) func() (xlog.TailOptions, io.Writer) {
	dom := fs.StringP("domain", "d", "", "Domain to filter logs in")
	minLevel := fs.StringP("min-level", "l", "info", "Minimum log level")
	after := fs.StringP("after", "a", "", "Time lower limit RFC3339, 'dd/mm/yyyy hh:mm UTC' or relative like '1h' for an hour ago")
	before := fs.StringP("before", "b", "", "Time upper limit RFC3339, 'dd/mm/yyyy hh:mm UTC' or relative like '1h' for an hour ago")
	search := fs.StringP("search", "s", "", "Substring filter")
	lineLimit := fs.Int64P("lines", "n", 100, "Max lines to emit from history")
	ioLimit := fs.Int64P("io-limit", "i", 1000, "Max MB allowed to read from history")
	nofollow := fs.Bool("no-follow", false, "Follow logs in real time")
	json := fs.BoolP("json", "j", config.IsTermDumb(), "Output logs in JSON format")
	noviral := fs.Bool("no-viral", false, "Viral logs, will broadcast the tail request to all nodes")
	return func() (o xlog.TailOptions, w io.Writer) {
		o.Domain = *dom
		if *minLevel != "info" {
			o.MinLevel.UnmarshalText([]byte(*minLevel))
		}
		if *after != "" {
			o.After, _ = parseFriendlyTime(*after)
		}
		if *before != "" {
			o.Before, _ = parseFriendlyTime(*before)
		}
		o.Search = *search
		o.LineLimit = *lineLimit
		o.IoLimit = *ioLimit * 1024 * 1024
		o.Follow = !*nofollow
		o.Viral = !*noviral
		if *json {
			w = os.Stdout
		} else {
			w = xlog.StdoutWriter()
		}
		return
	}
}

var tailCmd = &cobra.Command{
	Use:     "tail",
	Short:   "Tail logs",
	Args:    cobra.NoArgs,
	GroupID: refGroup("log", "Log Queries"),
}
var raytraceCmd = &cobra.Command{
	Use:     "raytrace [ray]",
	Short:   "Find logs by ray ID",
	Args:    cobra.ExactArgs(1),
	GroupID: refGroup("log", "Logs"),
}
var tailOptions = tailOptionsP(tailCmd.PersistentFlags())
var raytraceOptions = tailOptionsP(raytraceCmd.PersistentFlags())

func init() {
	raytraceCmd.RunE = func(cmd *cobra.Command, args []string) error {
		ctx, cancel := rundown.WithContext(context.Background())
		defer cancel()

		opt, wr := raytraceOptions()
		opt, err := opt.WithRay(args[0])
		if err != nil {
			return err
		}

		mw := xlog.ToMuxWriter(wr)
		defer mw.Flush()
		if cli := getClientIf(); cli.Valid() {
			err = cli.TailContext(ctx, opt, mw)
		} else {
			err = xlog.TailContext(ctx, opt, mw)
		}
		if err != nil {
			log.Fatal(err)
		}
		return nil
	}
	tailCmd.Run = func(cmd *cobra.Command, args []string) {
		ctx, cancel := rundown.WithContext(context.Background())
		defer cancel()

		opt, wr := tailOptions()
		mw := xlog.ToMuxWriter(wr)
		defer mw.Flush()

		if cli := getClientIf(); cli.Valid() {
			if err := cli.TailContext(ctx, opt, mw); err != nil {
				log.Fatal(err)
			}
		} else {
			wantsFollow := opt.Follow
			opt.Follow = false
			if err := xlog.TailContext(ctx, opt, mw); err != nil {
				log.Fatal(err)
			}
			if wantsFollow {
				opt.Follow = true
				opt.IoLimit = -1
				cli = getClient()
				if err := cli.TailContext(ctx, opt, mw); err != nil {
					log.Fatal(err)
				}
			}
		}
	}
	config.RootCommand.AddCommand(tailCmd, raytraceCmd)
}
