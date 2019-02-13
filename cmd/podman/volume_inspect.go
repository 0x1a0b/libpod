package main

import (
	"github.com/containers/libpod/cmd/podman/cliconfig"
	"github.com/containers/libpod/libpod/adapter"
	"github.com/pkg/errors"
	"github.com/spf13/cobra"
)

var (
	volumeInspectCommand     cliconfig.VolumeInspectValues
	volumeInspectDescription = `
podman volume inspect

Display detailed information on one or more volumes. Can change the format
from JSON to a Go template.
`
	_volumeInspectCommand = &cobra.Command{
		Use:   "inspect",
		Short: "Display detailed information on one or more volumes",
		Long:  volumeInspectDescription,
		RunE: func(cmd *cobra.Command, args []string) error {
			volumeInspectCommand.InputArgs = args
			volumeInspectCommand.GlobalFlags = MainGlobalOpts
			return volumeInspectCmd(&volumeInspectCommand)
		},
		Example: "[VOLUME-NAME ...]",
	}
)

func init() {
	volumeInspectCommand.Command = _volumeInspectCommand
	volumeInspectCommand.SetUsageTemplate(UsageTemplate())
	flags := volumeInspectCommand.Flags()
	flags.BoolVarP(&volumeInspectCommand.All, "all", "a", false, "Inspect all volumes")
	flags.StringVarP(&volumeInspectCommand.Format, "format", "f", "json", "Format volume output using Go template")

}

func volumeInspectCmd(c *cliconfig.VolumeInspectValues) error {
	if (c.All && len(c.InputArgs) > 0) || (!c.All && len(c.InputArgs) < 1) {
		return errors.New("provide one or more volume names or use --all")
	}

	runtime, err := adapter.GetRuntime(&c.PodmanCommand)
	if err != nil {
		return errors.Wrapf(err, "error creating libpod runtime")
	}
	defer runtime.Shutdown(false)

	vols, err := runtime.InspectVolumes(getContext(), c)
	if err != nil {
		return err
	}
	return generateVolLsOutput(vols, volumeLsOptions{Format: c.Format})
}
