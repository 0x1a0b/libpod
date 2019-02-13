package varlinkapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/containers/buildah"
	"github.com/containers/buildah/imagebuildah"
	"github.com/containers/image/docker"
	dockerarchive "github.com/containers/image/docker/archive"
	"github.com/containers/image/manifest"
	"github.com/containers/image/transports/alltransports"
	"github.com/containers/image/types"
	"github.com/containers/libpod/cmd/podman/shared"
	"github.com/containers/libpod/cmd/podman/varlink"
	"github.com/containers/libpod/libpod"
	"github.com/containers/libpod/libpod/image"
	sysreg "github.com/containers/libpod/pkg/registries"
	"github.com/containers/libpod/pkg/util"
	"github.com/containers/libpod/utils"
	"github.com/containers/storage/pkg/archive"
	"github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/opencontainers/runtime-spec/specs-go"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
)

// ListImages lists all the images in the store
// It requires no inputs.
func (i *LibpodAPI) ListImages(call iopodman.VarlinkCall) error {
	images, err := i.Runtime.ImageRuntime().GetImages()
	if err != nil {
		return call.ReplyErrorOccurred(fmt.Sprintf("unable to get list of images %q", err))
	}
	var imageList []iopodman.Image
	for _, image := range images {
		labels, _ := image.Labels(getContext())
		containers, _ := image.Containers()
		repoDigests, err := image.RepoDigests()
		if err != nil {
			return err
		}

		size, _ := image.Size(getContext())
		isParent, err := image.IsParent()
		if err != nil {
			return call.ReplyErrorOccurred(err.Error())
		}

		i := iopodman.Image{
			Id:          image.ID(),
			ParentId:    image.Parent,
			RepoTags:    image.Names(),
			RepoDigests: repoDigests,
			Created:     image.Created().Format(time.RFC3339),
			Size:        int64(*size),
			VirtualSize: image.VirtualSize,
			Containers:  int64(len(containers)),
			Labels:      labels,
			IsParent:    isParent,
		}
		imageList = append(imageList, i)
	}
	return call.ReplyListImages(imageList)
}

// GetImage returns a single image in the form of a Image
func (i *LibpodAPI) GetImage(call iopodman.VarlinkCall, id string) error {
	newImage, err := i.Runtime.ImageRuntime().NewFromLocal(id)
	if err != nil {
		return call.ReplyImageNotFound(id)
	}
	labels, err := newImage.Labels(getContext())
	if err != nil {
		return err
	}
	containers, err := newImage.Containers()
	if err != nil {
		return err
	}
	repoDigests, err := newImage.RepoDigests()
	if err != nil {
		return err
	}
	size, err := newImage.Size(getContext())
	if err != nil {
		return err
	}

	il := iopodman.Image{
		Id:          newImage.ID(),
		ParentId:    newImage.Parent,
		RepoTags:    newImage.Names(),
		RepoDigests: repoDigests,
		Created:     newImage.Created().Format(time.RFC3339),
		Size:        int64(*size),
		VirtualSize: newImage.VirtualSize,
		Containers:  int64(len(containers)),
		Labels:      labels,
	}
	return call.ReplyGetImage(il)
}

// BuildImage ...
func (i *LibpodAPI) BuildImage(call iopodman.VarlinkCall, config iopodman.BuildInfo) error {
	var (
		namespace []buildah.NamespaceOption
		err       error
	)

	systemContext := types.SystemContext{}
	contextDir := config.ContextDir

	newContextDir, err := ioutil.TempDir("", "buildTarball")
	if err != nil {
		call.ReplyErrorOccurred("unable to create tempdir")
	}
	logrus.Debugf("created new context dir at %s", newContextDir)

	reader, err := os.Open(contextDir)
	if err != nil {
		logrus.Errorf("failed to open the context dir tar file %s", contextDir)
		return call.ReplyErrorOccurred(fmt.Sprintf("unable to open context dir tar file %s", contextDir))
	}
	defer reader.Close()
	if err := archive.Untar(reader, newContextDir, &archive.TarOptions{}); err != nil {
		logrus.Errorf("fail to untar the context dir tarball (%s) to the context dir (%s)", contextDir, newContextDir)
		return call.ReplyErrorOccurred(fmt.Sprintf("unable to untar context dir %s", contextDir))
	}
	logrus.Debugf("untar of %s successful", contextDir)

	// All output (stdout, stderr) is captured in output as well
	output := bytes.NewBuffer([]byte{})

	commonOpts := &buildah.CommonBuildOptions{
		AddHost:      config.BuildOptions.AddHosts,
		CgroupParent: config.BuildOptions.CgroupParent,
		CPUPeriod:    uint64(config.BuildOptions.CpuPeriod),
		CPUQuota:     config.BuildOptions.CpuQuota,
		CPUSetCPUs:   config.BuildOptions.CpusetCpus,
		CPUSetMems:   config.BuildOptions.CpusetMems,
		Memory:       config.BuildOptions.Memory,
		MemorySwap:   config.BuildOptions.MemorySwap,
		ShmSize:      config.BuildOptions.ShmSize,
		Ulimit:       config.BuildOptions.Ulimit,
		Volumes:      config.BuildOptions.Volume,
	}

	hostNetwork := buildah.NamespaceOption{
		Name: specs.NetworkNamespace,
		Host: true,
	}

	namespace = append(namespace, hostNetwork)

	options := imagebuildah.BuildOptions{
		CommonBuildOpts:         commonOpts,
		AdditionalTags:          config.AdditionalTags,
		Annotations:             config.Annotations,
		Args:                    config.BuildArgs,
		CNIConfigDir:            config.CniConfigDir,
		CNIPluginPath:           config.CniPluginDir,
		Compression:             stringCompressionToArchiveType(config.Compression),
		ContextDirectory:        newContextDir,
		DefaultMountsFilePath:   config.DefaultsMountFilePath,
		Err:                     output,
		ForceRmIntermediateCtrs: config.ForceRmIntermediateCtrs,
		IIDFile:                 config.Iidfile,
		Labels:                  config.Label,
		Layers:                  config.Layers,
		NoCache:                 config.Nocache,
		Out:                     output,
		Output:                  config.Output,
		NamespaceOptions:        namespace,
		OutputFormat:            config.OutputFormat,
		PullPolicy:              stringPullPolicyToType(config.PullPolicy),
		Quiet:                   config.Quiet,
		RemoveIntermediateCtrs:  config.RemoteIntermediateCtrs,
		ReportWriter:            output,
		RuntimeArgs:             config.RuntimeArgs,
		SignaturePolicyPath:     config.SignaturePolicyPath,
		Squash:                  config.Squash,
		SystemContext:           &systemContext,
	}

	if call.WantsMore() {
		call.Continues = true
	}

	var newPathDockerFiles []string

	for _, d := range config.Dockerfiles {
		if strings.HasPrefix(d, "http://") ||
			strings.HasPrefix(d, "https://") ||
			strings.HasPrefix(d, "git://") ||
			strings.HasPrefix(d, "github.com/") {
			newPathDockerFiles = append(newPathDockerFiles, d)
			continue
		}
		base := filepath.Base(d)
		newPathDockerFiles = append(newPathDockerFiles, filepath.Join(newContextDir, base))
	}

	c := build(i.Runtime, options, newPathDockerFiles)
	var log []string
	done := false
	for {
		outputLine, err := output.ReadString('\n')
		if err == nil {
			log = append(log, outputLine)
			if call.WantsMore() {
				//	 we want to reply with what we have
				br := iopodman.MoreResponse{
					Logs: log,
				}
				call.ReplyBuildImage(br)
				log = []string{}
			}
			continue
		} else if err == io.EOF {
			select {
			case err := <-c:
				if err != nil {
					return call.ReplyErrorOccurred(err.Error())
				}
				done = true
			default:
				if call.WantsMore() {
					time.Sleep(1 * time.Second)
					break
				}
			}
		} else {
			return call.ReplyErrorOccurred(err.Error())
		}
		if done {
			break
		}
	}
	call.Continues = false

	newImage, err := i.Runtime.ImageRuntime().NewFromLocal(config.Output)
	if err != nil {
		return call.ReplyErrorOccurred(err.Error())
	}
	br := iopodman.MoreResponse{
		Logs: log,
		Id:   newImage.ID(),
	}
	return call.ReplyBuildImage(br)
}

func build(runtime *libpod.Runtime, options imagebuildah.BuildOptions, dockerfiles []string) chan error {
	c := make(chan error)
	go func() {
		err := runtime.Build(getContext(), options, dockerfiles...)
		c <- err
		close(c)
	}()

	return c
}

// InspectImage returns an image's inspect information as a string that can be serialized.
// Requires an image ID or name
func (i *LibpodAPI) InspectImage(call iopodman.VarlinkCall, name string) error {
	newImage, err := i.Runtime.ImageRuntime().NewFromLocal(name)
	if err != nil {
		return call.ReplyImageNotFound(name)
	}
	inspectInfo, err := newImage.Inspect(getContext())
	if err != nil {
		return call.ReplyErrorOccurred(err.Error())
	}
	b, err := json.Marshal(inspectInfo)
	if err != nil {
		return call.ReplyErrorOccurred(fmt.Sprintf("unable to serialize"))
	}
	return call.ReplyInspectImage(string(b))
}

// HistoryImage returns the history of the image's layers
// Requires an image or name
func (i *LibpodAPI) HistoryImage(call iopodman.VarlinkCall, name string) error {
	newImage, err := i.Runtime.ImageRuntime().NewFromLocal(name)
	if err != nil {
		return call.ReplyImageNotFound(name)
	}
	history, err := newImage.History(getContext())
	if err != nil {
		return call.ReplyErrorOccurred(err.Error())
	}
	var histories []iopodman.ImageHistory
	for _, hist := range history {
		imageHistory := iopodman.ImageHistory{
			Id:        hist.ID,
			Created:   hist.Created.Format(time.RFC3339),
			CreatedBy: hist.CreatedBy,
			Tags:      newImage.Names(),
			Size:      hist.Size,
			Comment:   hist.Comment,
		}
		histories = append(histories, imageHistory)
	}
	return call.ReplyHistoryImage(histories)
}

// PushImage pushes an local image to registry
func (i *LibpodAPI) PushImage(call iopodman.VarlinkCall, name, tag string, tlsVerify bool, signaturePolicy, creds, certDir string, compress bool, format string, removeSignatures bool, signBy string) error {
	var (
		registryCreds *types.DockerAuthConfig
		manifestType  string
	)
	newImage, err := i.Runtime.ImageRuntime().NewFromLocal(name)
	if err != nil {
		return call.ReplyImageNotFound(err.Error())
	}
	destname := name
	if tag != "" {
		destname = tag
	}
	if creds != "" {
		creds, err := util.ParseRegistryCreds(creds)
		if err != nil {
			return err
		}
		registryCreds = creds
	}
	dockerRegistryOptions := image.DockerRegistryOptions{
		DockerRegistryCreds: registryCreds,
		DockerCertPath:      certDir,
	}
	if !tlsVerify {
		dockerRegistryOptions.DockerInsecureSkipTLSVerify = types.OptionalBoolTrue
	}
	if format != "" {
		switch format {
		case "oci": //nolint
			manifestType = v1.MediaTypeImageManifest
		case "v2s1":
			manifestType = manifest.DockerV2Schema1SignedMediaType
		case "v2s2", "docker":
			manifestType = manifest.DockerV2Schema2MediaType
		default:
			return call.ReplyErrorOccurred(fmt.Sprintf("unknown format %q. Choose on of the supported formats: 'oci', 'v2s1', or 'v2s2'", format))
		}
	}
	so := image.SigningOptions{
		RemoveSignatures: removeSignatures,
		SignBy:           signBy,
	}

	if call.WantsMore() {
		call.Continues = true
	}

	output := bytes.NewBuffer([]byte{})
	c := make(chan error)
	go func() {
		err := newImage.PushImageToHeuristicDestination(getContext(), destname, manifestType, "", signaturePolicy, output, compress, so, &dockerRegistryOptions, nil)
		c <- err
		close(c)
	}()

	// TODO When pull output gets fixed for the remote client, we need to look into how we can turn below
	// into something re-usable.  it is in build too
	var log []string
	done := false
	for {
		line, err := output.ReadString('\n')
		if err == nil {
			log = append(log, line)
			continue
		} else if err == io.EOF {
			select {
			case err := <-c:
				if err != nil {
					logrus.Errorf("reading of output during push failed for %s", newImage.ID())
					return call.ReplyErrorOccurred(err.Error())
				}
				done = true
			default:
				if !call.WantsMore() {
					time.Sleep(1 * time.Second)
					break
				}
				br := iopodman.MoreResponse{
					Logs: log,
				}
				call.ReplyPushImage(br)
				log = []string{}
			}
		} else {
			return call.ReplyErrorOccurred(err.Error())
		}
		if done {
			break
		}
	}
	call.Continues = false

	br := iopodman.MoreResponse{
		Logs: log,
	}
	return call.ReplyPushImage(br)
}

// TagImage accepts an image name and tag as strings and tags an image in the local store.
func (i *LibpodAPI) TagImage(call iopodman.VarlinkCall, name, tag string) error {
	newImage, err := i.Runtime.ImageRuntime().NewFromLocal(name)
	if err != nil {
		return call.ReplyImageNotFound(name)
	}
	if err := newImage.TagImage(tag); err != nil {
		return call.ReplyErrorOccurred(err.Error())
	}
	return call.ReplyTagImage(newImage.ID())
}

// RemoveImage accepts a image name or ID as a string and force bool to determine if it should
// remove the image even if being used by stopped containers
func (i *LibpodAPI) RemoveImage(call iopodman.VarlinkCall, name string, force bool) error {
	ctx := getContext()
	newImage, err := i.Runtime.ImageRuntime().NewFromLocal(name)
	if err != nil {
		return call.ReplyImageNotFound(name)
	}
	_, err = i.Runtime.RemoveImage(ctx, newImage, force)
	if err != nil {
		return call.ReplyErrorOccurred(err.Error())
	}
	return call.ReplyRemoveImage(newImage.ID())
}

// SearchImages searches all registries configured in /etc/containers/registries.conf for an image
// Requires an image name and a search limit as int
func (i *LibpodAPI) SearchImages(call iopodman.VarlinkCall, query string, limit *int64) error {
	sc := image.GetSystemContext("", "", false)
	registries, err := sysreg.GetRegistries()
	if err != nil {
		return call.ReplyErrorOccurred(fmt.Sprintf("unable to get system registries: %q", err))
	}
	var imageResults []iopodman.ImageSearchResult
	for _, reg := range registries {
		var lim = 1000
		if limit != nil {
			lim = int(*limit)
		}
		results, err := docker.SearchRegistry(getContext(), sc, reg, query, lim)
		if err != nil {
			// If we are searching multiple registries, don't make something like an
			// auth error fatal. Unfortunately we cannot differentiate between auth
			// errors and other possibles errors
			if len(registries) > 1 {
				continue
			}
			return call.ReplyErrorOccurred(err.Error())
		}
		for _, result := range results {
			i := iopodman.ImageSearchResult{
				Description:  result.Description,
				Is_official:  result.IsOfficial,
				Is_automated: result.IsAutomated,
				Name:         result.Name,
				Star_count:   int64(result.StarCount),
			}
			imageResults = append(imageResults, i)
		}
	}
	return call.ReplySearchImages(imageResults)
}

// DeleteUnusedImages deletes any images that do not have containers associated with it.
// TODO Filters are not implemented
func (i *LibpodAPI) DeleteUnusedImages(call iopodman.VarlinkCall) error {
	images, err := i.Runtime.ImageRuntime().GetImages()
	if err != nil {
		return call.ReplyErrorOccurred(err.Error())
	}
	var deletedImages []string
	for _, img := range images {
		containers, err := img.Containers()
		if err != nil {
			return call.ReplyErrorOccurred(err.Error())
		}
		if len(containers) == 0 {
			if err := img.Remove(false); err != nil {
				return call.ReplyErrorOccurred(err.Error())
			}
			deletedImages = append(deletedImages, img.ID())
		}
	}
	return call.ReplyDeleteUnusedImages(deletedImages)
}

// Commit ...
func (i *LibpodAPI) Commit(call iopodman.VarlinkCall, name, imageName string, changes []string, author, message string, pause bool, manifestType string) error {
	ctr, err := i.Runtime.LookupContainer(name)
	if err != nil {
		return call.ReplyContainerNotFound(name)
	}
	sc := image.GetSystemContext(i.Runtime.GetConfig().SignaturePolicyPath, "", false)
	var mimeType string
	switch manifestType {
	case "oci", "": //nolint
		mimeType = buildah.OCIv1ImageManifest
	case "docker":
		mimeType = manifest.DockerV2Schema2MediaType
	default:
		return call.ReplyErrorOccurred(fmt.Sprintf("unrecognized image format %q", manifestType))
	}
	coptions := buildah.CommitOptions{
		SignaturePolicyPath:   i.Runtime.GetConfig().SignaturePolicyPath,
		ReportWriter:          nil,
		SystemContext:         sc,
		PreferredManifestType: mimeType,
	}
	options := libpod.ContainerCommitOptions{
		CommitOptions: coptions,
		Pause:         pause,
		Message:       message,
		Changes:       changes,
		Author:        author,
	}

	newImage, err := ctr.Commit(getContext(), imageName, options)
	if err != nil {
		return call.ReplyErrorOccurred(err.Error())
	}
	return call.ReplyCommit(newImage.ID())
}

// ImportImage imports an image from a tarball to the image store
func (i *LibpodAPI) ImportImage(call iopodman.VarlinkCall, source, reference, message string, changes []string, delete bool) error {
	configChanges, err := util.GetImageConfig(changes)
	if err != nil {
		return call.ReplyErrorOccurred(err.Error())
	}
	history := []v1.History{
		{Comment: message},
	}
	config := v1.Image{
		Config:  configChanges,
		History: history,
	}
	newImage, err := i.Runtime.ImageRuntime().Import(getContext(), source, reference, nil, image.SigningOptions{}, config)
	if err != nil {
		return call.ReplyErrorOccurred(err.Error())
	}
	if delete {
		if err := os.Remove(source); err != nil {
			return call.ReplyErrorOccurred(err.Error())
		}
	}

	return call.ReplyImportImage(newImage.ID())
}

// ExportImage exports an image to the provided destination
// destination must have the transport type!!
func (i *LibpodAPI) ExportImage(call iopodman.VarlinkCall, name, destination string, compress bool, tags []string) error {
	newImage, err := i.Runtime.ImageRuntime().NewFromLocal(name)
	if err != nil {
		return call.ReplyImageNotFound(name)
	}

	additionalTags, err := image.GetAdditionalTags(tags)
	if err != nil {
		return err
	}

	if err := newImage.PushImageToHeuristicDestination(getContext(), destination, "", "", "", nil, compress, image.SigningOptions{}, &image.DockerRegistryOptions{}, additionalTags); err != nil {
		return call.ReplyErrorOccurred(err.Error())
	}
	return call.ReplyExportImage(newImage.ID())
}

// PullImage pulls an image from a registry to the image store.
func (i *LibpodAPI) PullImage(call iopodman.VarlinkCall, name string, certDir, creds, signaturePolicy string, tlsVerify bool) error {
	var (
		registryCreds *types.DockerAuthConfig
		imageID       string
	)
	if creds != "" {
		creds, err := util.ParseRegistryCreds(creds)
		if err != nil {
			return err
		}
		registryCreds = creds
	}

	dockerRegistryOptions := image.DockerRegistryOptions{
		DockerRegistryCreds: registryCreds,
		DockerCertPath:      certDir,
	}
	if tlsVerify {
		dockerRegistryOptions.DockerInsecureSkipTLSVerify = types.NewOptionalBool(!tlsVerify)
	}

	so := image.SigningOptions{}

	if strings.HasPrefix(name, dockerarchive.Transport.Name()+":") {
		srcRef, err := alltransports.ParseImageName(name)
		if err != nil {
			return errors.Wrapf(err, "error parsing %q", name)
		}
		newImage, err := i.Runtime.ImageRuntime().LoadFromArchiveReference(getContext(), srcRef, signaturePolicy, nil)
		if err != nil {
			return errors.Wrapf(err, "error pulling image from %q", name)
		}
		imageID = newImage[0].ID()
	} else {
		newImage, err := i.Runtime.ImageRuntime().New(getContext(), name, signaturePolicy, "", nil, &dockerRegistryOptions, so, false, nil)
		if err != nil {
			return call.ReplyErrorOccurred(fmt.Sprintf("unable to pull %s: %s", name, err.Error()))
		}
		imageID = newImage.ID()
	}
	return call.ReplyPullImage(imageID)
}

// ImageExists returns bool as to whether the input image exists in local storage
func (i *LibpodAPI) ImageExists(call iopodman.VarlinkCall, name string) error {
	_, err := i.Runtime.ImageRuntime().NewFromLocal(name)
	if errors.Cause(err) == image.ErrNoSuchImage {
		return call.ReplyImageExists(1)
	}
	if err != nil {
		return call.ReplyErrorOccurred(err.Error())
	}
	return call.ReplyImageExists(0)
}

// ContainerRunlabel ...
func (i *LibpodAPI) ContainerRunlabel(call iopodman.VarlinkCall, input iopodman.Runlabel) error {
	ctx := getContext()
	dockerRegistryOptions := image.DockerRegistryOptions{
		DockerCertPath: input.CertDir,
	}
	if !input.TlsVerify {
		dockerRegistryOptions.DockerInsecureSkipTLSVerify = types.OptionalBoolTrue
	}

	stdErr := os.Stderr
	stdOut := os.Stdout
	stdIn := os.Stdin

	runLabel, imageName, err := shared.GetRunlabel(input.Label, input.Image, ctx, i.Runtime, input.Pull, input.Creds, dockerRegistryOptions, input.Authfile, input.SignaturePolicyPath, nil)
	if err != nil {
		return call.ReplyErrorOccurred(err.Error())
	}
	if runLabel == "" {
		return call.ReplyErrorOccurred(fmt.Sprintf("%s does not contain the label %s", input.Image, input.Label))
	}

	cmd, env, err := shared.GenerateRunlabelCommand(runLabel, imageName, input.Name, input.Opts, input.ExtraArgs)
	if err != nil {
		return call.ReplyErrorOccurred(err.Error())
	}
	if err := utils.ExecCmdWithStdStreams(stdIn, stdOut, stdErr, env, cmd[0], cmd[1:]...); err != nil {
		return call.ReplyErrorOccurred(err.Error())
	}
	return call.ReplyContainerRunlabel()
}

// ImagesPrune ....
func (i *LibpodAPI) ImagesPrune(call iopodman.VarlinkCall, all bool) error {
	prunedImages, err := i.Runtime.ImageRuntime().PruneImages(all)
	if err != nil {
		return call.ReplyErrorOccurred(err.Error())
	}
	return call.ReplyImagesPrune(prunedImages)
}
