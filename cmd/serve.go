package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/iyear/tdl/core/logctx"
	"github.com/iyear/tdl/pkg/consts"
	"github.com/iyear/tdl/pkg/kv"
	"github.com/iyear/tdl/server"
)

func NewServe() *cobra.Command {
	var (
		port     int
		webRoot  string
		dir      string
		noBrowse bool
	)

	cmd := &cobra.Command{
		Use:     "serve",
		Short:   "Run the tdl web service (HTTP server + web UI)",
		GroupID: groupTools.ID,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := logctx.Named(cmd.Context(), "serve")

			stg := kv.From(ctx)

			if dir == "" {
				dir = defaultDownloadDir()
			}

			opts := server.Options{
				Addr:       fmt.Sprintf("127.0.0.1:%d", port),
				WebRoot:    webRoot,
				DefaultDir: dir,
				DataDir:    filepath.Join(consts.DataDir),
				Proxy:      viper.GetString(consts.FlagProxy),
				NTP:        viper.GetString(consts.FlagNTP),
			}

			s := server.New(opts, stg)

			if !noBrowse {
				openBrowser(fmt.Sprintf("http://127.0.0.1:%d", port))
			}

			logctx.From(ctx).Sugar().Infof("tdl service listening on http://127.0.0.1:%d", port)
			return s.Run(ctx)
		},
	}

	cmd.Flags().IntVar(&port, "port", 8080, "port to listen on (bound to 127.0.0.1)")
	cmd.Flags().StringVar(&webRoot, "web-root", "", "directory of the built web frontend (default: <exe dir>/web)")
	cmd.Flags().StringVarP(&dir, "dir", "d", "", "default download directory (default: <exe dir>/downloads)")
	cmd.Flags().BoolVar(&noBrowse, "no-browser", false, "do not open the browser automatically")

	return cmd
}

// defaultDownloadDir mirrors server.defaultDownloadDir for the flag default.
func defaultDownloadDir() string {
	exe, err := os.Executable()
	if err != nil {
		return "downloads"
	}
	return filepath.Join(filepath.Dir(exe), "downloads")
}
