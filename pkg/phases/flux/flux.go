package flux

import (
	"encoding/base64"
	"fmt"
	"sort"

	log "github.com/sirupsen/logrus"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/moshloop/platform-cli/pkg/k8s"
	"github.com/moshloop/platform-cli/pkg/platform"
	"github.com/moshloop/platform-cli/pkg/types"
)

func Install(p *platform.Platform) error {
	log.Infof("Deploying %d gitops controllers", len(p.GitOps))
	for _, gitops := range p.GitOps {
		if err := p.Apply(gitops.Namespace, NewFluxDeployment(&gitops)...); err != nil {
			return err
		}
	}
	return nil
}

// Create flux command arguments from CR
func defaults(cr *types.GitOps) {
	if cr.Namespace == "" {
		cr.Namespace = "kube-system"
	}
	if cr.Name == "" {
		cr.Name = cr.Namespace
	}

	if cr.GitBranch == "" {
		cr.GitBranch = "master"
	}

	if cr.GitPath == "" {
		cr.GitPath = "./"
	}

	if cr.GitPollInterval == "" {
		cr.GitPollInterval = "60s"
	}

	if cr.SyncInterval == "" {
		cr.SyncInterval = "5m00s"
	}

	if cr.FluxVersion == "" {
		cr.FluxVersion = "1.16.0"
	}
}

func getArgs(cr *types.GitOps, argMap map[string]string) []string {
	var args []string
	for key, value := range cr.Args {
		argMap[key] = value
	}
	for key, value := range argMap {
		args = append(args, fmt.Sprintf("--%s=%s", key, value))
	}

	sort.Strings(args)
	return args
}

// NewFluxDeployment creates a new flux pod
func NewFluxDeployment(cr *types.GitOps) []runtime.Object {
	defaults(cr)
	memcacheName := fmt.Sprintf("flux-memcache-%s", cr.Name)
	secretName := fmt.Sprintf("flux-git-deploy-%s", cr.Name)
	sshConfig := fmt.Sprintf("flux-ssh-%s", cr.Name)
	saName := fmt.Sprintf("flux-" + cr.Name)
	argMap := map[string]string{
		"git-url":                cr.GitUrl,
		"git-branch":             cr.GitBranch,
		"git-path":               cr.GitPath,
		"git-poll-interval":      cr.GitPollInterval,
		"git-readonly":           "true",
		"sync-interval":          cr.SyncInterval,
		"k8s-secret-name":        secretName,
		"ssh-keygen-dir":         "/etc/fluxd/ssh",
		"memcached-hostname":     memcacheName,
		"registry-exclude-image": "*",
		// use ClusterIP rather than DNS SRV lookup
		"memcached-service": "",
	}

	spec := k8s.Builder{
		Namespace: cr.Namespace,
		Labels: map[string]string{
			"app": "flux",
		},
	}

	spec.Deployment(memcacheName, "docker.io/memcached:1.4.36-alpine").
		Args("-m 512", "-p 11211", "-I 5m").
		Expose(11211).
		Build()

	spec.Deployment("flux-"+cr.Name, fmt.Sprintf("%s:%s", "docker.io/fluxcd/flux", cr.FluxVersion)).
		Args(getArgs(cr, argMap)...).
		ServiceAccount(saName).
		MountSecret(secretName, "/etc/fluxd/ssh", int32(0400)).
		MountConfigMap(sshConfig, "/root/.ssh").
		Expose(3030).
		Build()

	if cr.Namespace == "kube-system" {
		spec.ServiceAccount(saName).AddClusterRole("cluster-admin")
	} else {
		spec.ServiceAccount(saName).AddRole("cluster-admin")
	}

	data, _ := base64.StdEncoding.DecodeString(cr.GitKey)
	spec.Secret(secretName, map[string][]byte{
		"identity": data,
	})
	spec.ConfigMap(sshConfig, map[string]string{
		"known_hosts": cr.KnownHosts,
		"config":      cr.SSHConfig,
	})

	return spec.Objects
}
