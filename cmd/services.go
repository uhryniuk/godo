package cmd

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/uhryniuk/godo/internal/config"
	"github.com/uhryniuk/godo/internal/service"
)

var servicesCmd = &cobra.Command{
	Use:     "services",
	Aliases: []string{"svc", "svcs", "service"},
	Short:   "List installed service files",
	Args:    cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		config.InitConfig()
		rows := gatherServiceRows(config.GetServiceDir())
		renderServicesTable(os.Stdout, rows)
	},

}

type serviceRow struct {
	Name      string
	Command   string
	Autostart string
	Restart   string
	Cron      string
	File      string
	Status    string
}

// gatherServiceRows loads all *.toml files from dir and returns one row
// per file, including error rows for files that fail to parse.
func gatherServiceRows(dir string) []serviceRow {
	specs, perFileErrs, _ := service.LoadAllWithErrors(dir)

	var rows []serviceRow

	for _, s := range specs {
		restart := s.Restart
		if restart == "" {
			restart = "no"
		}
		cron := s.Cron.Schedule
		if cron == "" {
			cron = "-"
		}
		autostart := "no"
		if s.Autostart {
			autostart = "yes"
		}
		cmd := s.Command
		if len(s.Args) > 0 {
			cmd += " " + strings.Join(s.Args, " ")
		}
		rows = append(rows, serviceRow{
			Name:      s.Name,
			Command:   cmd,
			Autostart: autostart,
			Restart:   restart,
			Cron:      cron,
			File:      filepath.Base(s.Path),
			Status:    "ok",
		})
	}

	for path, err := range perFileErrs {
		rows = append(rows, serviceRow{
			File:   filepath.Base(path),
			Status: "error: " + err.Error(),
		})
	}

	sort.Slice(rows, func(i, j int) bool {
		ki := rows[i].Name
		if ki == "" {
			ki = rows[i].File
		}
		kj := rows[j].Name
		if kj == "" {
			kj = rows[j].File
		}
		return ki < kj
	})

	return rows
}

func renderServicesTable(w io.Writer, rows []serviceRow) {
	if len(rows) == 0 {
		fmt.Fprintln(w, "(no services)")
		return
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tCOMMAND\tAUTOSTART\tRESTART\tCRON\tFILE\tSTATUS")
	for _, r := range rows {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			r.Name, r.Command, r.Autostart, r.Restart, r.Cron, r.File, r.Status)
	}
	_ = tw.Flush()
}
