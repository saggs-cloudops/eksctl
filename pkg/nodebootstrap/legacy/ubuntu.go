package legacy

import (
	"fmt"
	"strings"

	"github.com/kris-nova/logger"
	"github.com/pkg/errors"
	api "github.com/weaveworks/eksctl/pkg/apis/eksctl.io/v1alpha5"
	"github.com/weaveworks/eksctl/pkg/cloudconfig"
	"github.com/weaveworks/eksctl/pkg/utils/kubeconfig"

	// Import go:embed
	_ "embed"
)

//go:embed scripts/bootstrap.legacy.ubuntu.sh
var bootstrapLegacyUbuntuShBytes []byte

const ubuntu2004ResolveConfPath = "/run/systemd/resolve/resolv.conf"

type UbuntuBootstrapper struct {
	clusterSpec *api.ClusterConfig
	ng          *api.NodeGroup
}

func NewUbuntuBootstrapper(clusterSpec *api.ClusterConfig, ng *api.NodeGroup) UbuntuBootstrapper {
	return UbuntuBootstrapper{
		clusterSpec: clusterSpec,
		ng:          ng,
	}
}

func (b UbuntuBootstrapper) UserData() (string, error) {
	config := cloudconfig.New()

	files, err := makeUbuntuConfig(b.clusterSpec, b.ng)
	if err != nil {
		return "", err
	}

	var scripts []script

	for _, command := range b.ng.PreBootstrapCommands {
		config.AddShellCommand(command)
	}

	if b.ng.OverrideBootstrapCommand != nil {
		config.AddShellCommand(*b.ng.OverrideBootstrapCommand)
	} else {
		scripts = append(scripts, script{name: "bootstrap.legacy.ubuntu.sh", contents: string(bootstrapLegacyUbuntuShBytes)})
	}

	if err = addFilesAndScripts(config, files, scripts); err != nil {
		return "", err
	}

	body, err := config.Encode()
	if err != nil {
		return "", errors.Wrap(err, "encoding user data")
	}

	logger.Debug("user-data = %s", body)
	return body, nil
}

func makeUbuntuConfig(spec *api.ClusterConfig, ng *api.NodeGroup) ([]configFile, error) {
	clientConfigData, err := makeClientConfigData(spec, kubeconfig.AWSEKSAuthenticator)
	if err != nil {
		return nil, err
	}

	if len(spec.Status.CertificateAuthorityData) == 0 {
		return nil, errors.New("invalid cluster config: missing CertificateAuthorityData")
	}

	kubeletEnvParams := makeCommonKubeletEnvParams(ng)

	if ng.ClusterDNS != "" {
		kubeletEnvParams = append(kubeletEnvParams, fmt.Sprintf("CLUSTER_DNS=%s", ng.ClusterDNS))
	}

	// Set resolvConf for Ubuntu 20.04 only, do not override user set value
	if ng.AMIFamily == api.NodeImageFamilyUbuntu2004 {
		if ng.KubeletExtraConfig == nil {
			ng.KubeletExtraConfig = &api.InlineDocument{}
		}
		if _, ok := (*ng.KubeletExtraConfig)["resolvConf"]; !ok {
			(*ng.KubeletExtraConfig)["resolvConf"] = ubuntu2004ResolveConfPath
		}
	}

	kubeletConfigData, err := makeKubeletConfigYAML(spec, ng)
	if err != nil {
		return nil, err
	}

	files := []configFile{{
		dir:      configDir,
		name:     "metadata.env",
		contents: strings.Join(makeMetadata(spec), "\n"),
	}, {
		dir:      configDir,
		name:     "kubelet.env",
		contents: strings.Join(kubeletEnvParams, "\n"),
	}, {
		dir:      configDir,
		name:     "kubelet.yaml",
		contents: string(kubeletConfigData),
	}, {
		dir:      configDir,
		name:     "ca.crt",
		contents: string(spec.Status.CertificateAuthorityData),
	}, {
		dir:      configDir,
		name:     "kubeconfig.yaml",
		contents: string(clientConfigData),
	}, {
		dir:      configDir,
		name:     "max_pods.map",
		contents: makeMaxPodsMapping(),
	}}

	return files, nil
}
