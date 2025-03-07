package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-openapi/swag"
	"github.com/openshift/appliance/pkg/graph"
	"github.com/openshift/appliance/pkg/types"
	"github.com/openshift/installer/pkg/asset"
	"github.com/openshift/installer/pkg/validate"
	"github.com/pkg/errors"
	"github.com/thoas/go-funk"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"sigs.k8s.io/yaml"
)

const (
	ApplianceConfigFilename = "appliance-config.yaml"

	// CPU architectures
	CpuArchitectureX86     = "x86_64"
	CpuArchitectureAARCH64 = "aarch64"
	CpuArchitecturePPC64le = "ppc64le"

	// Release architecture
	ReleaseArchitectureAMD64   = "amd64"
	ReleaseArchitectureARM64   = "arm64"
	ReleaseArchitecturePPC64le = "ppc64le"
)

var (
	cpuArchitectures = []string{CpuArchitectureX86, CpuArchitectureAARCH64, CpuArchitecturePPC64le}
)

// ApplianceConfig reads the appliance-config.yaml file.
type ApplianceConfig struct {
	File     *asset.File
	Config   *types.ApplianceConfig
	Template string
}

var _ asset.WritableAsset = (*ApplianceConfig)(nil)

// Name returns a human friendly name for the asset.
func (*ApplianceConfig) Name() string {
	return "Appliance Config"
}

// Dependencies returns all the dependencies directly needed to generate
// the asset.
func (*ApplianceConfig) Dependencies() []asset.Asset {
	return []asset.Asset{}
}

// Generate generates the Agent Config manifest.
func (a *ApplianceConfig) Generate(dependencies asset.Parents) error {
	applianceConfigTemplate := `#
# Note: This is a sample ApplianceConfig file showing
# which fields are available to aid you in creating your
# own appliance-config.yaml file.
#
apiVersion: v1beta1
kind: ApplianceConfig
ocpRelease:
	# OCP release version in major.minor or major.minor.patch format
	# (in case of major.minor - latest patch version will be used)
	version: ocp-release-version 
	# OCP release update channel: stable|fast|eus|candidate
	# Default: stable
	# [Optional] 
	channel: ocp-release-channel
	# OCP release CPU architecture: x86_64|aarch64|ppc64le
	# Default: x86_64
	# [Optional]
	cpuArchitecture: cpu-architecture
# Virtual size of the appliance disk image
diskSizeGB: disk-size
# PullSecret required for mirroring the OCP release payload
pullSecret: pull-secret
# Public SSH key for accessing the appliance
# [Optional]
sshKey: ssh-key
`
	a.Template = applianceConfigTemplate

	return nil
}

// PersistToFile writes the appliance-config.yaml file to the assets folder
func (a *ApplianceConfig) PersistToFile(directory string) error {
	if a.Template == "" {
		return nil
	}

	templatePath := filepath.Join(directory, ApplianceConfigFilename)
	templateByte := []byte(a.Template)
	err := os.WriteFile(templatePath, templateByte, 0644)
	if err != nil {
		return err
	}

	return nil
}

// Files returns the files generated by the asset.
func (a *ApplianceConfig) Files() []*asset.File {
	if a.File != nil {
		return []*asset.File{a.File}
	}
	return []*asset.File{}
}

// Load returns agent config asset from the disk.
func (a *ApplianceConfig) Load(f asset.FileFetcher) (bool, error) {
	file, err := f.FetchByName(ApplianceConfigFilename)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, errors.Wrap(err, fmt.Sprintf("failed to load %s file", ApplianceConfigFilename))
	}

	config := &types.ApplianceConfig{}
	if err := yaml.UnmarshalStrict(file.Data, config); err != nil {
		return false, errors.Wrapf(err, "failed to unmarshal %s", ApplianceConfigFilename)
	}

	// Fallback to x86_64
	if config.OcpRelease.CpuArchitecture == nil {
		config.OcpRelease.CpuArchitecture = swag.String(CpuArchitectureX86)
	}

	cpuArch := strings.ToLower(*config.OcpRelease.CpuArchitecture)
	if !funk.Contains(cpuArchitectures, cpuArch) {
		return false, errors.Errorf("Unsupported CPU architecture: %s", cpuArch)
	}
	config.OcpRelease.CpuArchitecture = swag.String(cpuArch)
	releaseArch := GetReleaseArchitectureByCPU(cpuArch)

	g := graph.NewGraph()
	releaseImage, releaseVersion, err := g.GetReleaseImage(config.OcpRelease.Version, config.OcpRelease.Channel, releaseArch)
	if err != nil {
		return false, err
	}
	config.OcpRelease.URL = &releaseImage
	config.OcpRelease.Version = releaseVersion

	a.File, a.Config = file, config
	if err = a.finish(); err != nil {
		return false, err
	}

	return true, nil
}

func (a *ApplianceConfig) finish() error {
	if err := a.validateConfig().ToAggregate(); err != nil {
		return errors.Wrapf(err, "invalid Appliance Config configuration")
	}

	return nil
}

func (a *ApplianceConfig) validateConfig() field.ErrorList {
	allErrs := field.ErrorList{}
	if a.Config.TypeMeta.APIVersion == "" {
		return field.ErrorList{field.Required(field.NewPath("apiVersion"), "install-config version required")}
	}
	switch v := a.Config.APIVersion; v {
	case types.ApplianceConfigVersion:
	// Current version
	default:
		return field.ErrorList{field.Invalid(field.NewPath("apiVersion"), a.Config.TypeMeta.APIVersion, fmt.Sprintf("appliance-config version must be %q", types.ApplianceConfigVersion))}
	}

	if err := validate.ImagePullSecret(a.Config.PullSecret); err != nil {
		allErrs = append(allErrs, field.Invalid(field.NewPath("pullSecret"), a.Config.PullSecret, err.Error()))
	}

	return allErrs
}

func (a *ApplianceConfig) GetCpuArchitecture() string {
	// Note: in Load func, we ensure that CpuArchitecture is not nil and fallback to x86_64
	return swag.StringValue(a.Config.OcpRelease.CpuArchitecture)
}

func GetReleaseArchitectureByCPU(arch string) string {
	switch arch {
	case CpuArchitectureX86:
		return ReleaseArchitectureAMD64
	case CpuArchitectureAARCH64:
		return ReleaseArchitectureARM64
	default:
		return arch
	}
}
