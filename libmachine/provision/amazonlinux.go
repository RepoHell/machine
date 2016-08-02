package provision

import (
	"fmt"
	"github.com/docker/machine/libmachine/auth"
	"github.com/docker/machine/libmachine/drivers"
	"github.com/docker/machine/libmachine/engine"
	"github.com/docker/machine/libmachine/log"
	"github.com/docker/machine/libmachine/mcnutils"
	"github.com/docker/machine/libmachine/provision/pkgaction"
	"github.com/docker/machine/libmachine/provision/serviceaction"
	"github.com/docker/machine/libmachine/swarm"
	"strings"
)

func init() {
	Register("AmazonLinux", &RegisteredProvisioner{
		New: NewAmazonLinuxProvisioner,
	})
}

func NewAmazonLinuxProvisioner(d drivers.Driver) Provisioner {
	return &AmazonLinuxProvisioner{
		GenericProvisioner{
			SSHCommander:         GenericSSHCommander{Driver: d},
			DockerOptionsDir:     "/etc/sysconfig",
			DaemonOptionsFile:    "/etc/sysconfig/docker",
			DockerOptionsVarName: "OPTIONS",
			OsReleaseID:          "amzn",
			Packages: []string{
				"curl",
			},
			Driver: d,
		},
	}
}

type AmazonLinuxProvisioner struct {
	GenericProvisioner
}

func (provisioner *AmazonLinuxProvisioner) String() string {
	return "amazon(upstart)"
}

func (provisioner *AmazonLinuxProvisioner) CompatibleWithHost() bool {
	isAmazon := provisioner.OsReleaseInfo.ID == provisioner.OsReleaseID
	if !isAmazon {
		return false
	}
	return true
}

func (provisioner *AmazonLinuxProvisioner) Service(name string, action serviceaction.ServiceAction) error {
	command := fmt.Sprintf("sudo service %s %s", name, action.String())

	if _, err := provisioner.SSHCommand(command); err != nil {
		return err
	}

	return nil
}

func (provisioner *AmazonLinuxProvisioner) Package(name string, action pkgaction.PackageAction) error {
	var packageAction string

	switch action {
	case pkgaction.Install:
		packageAction = "install"
	case pkgaction.Remove:
		packageAction = "remove"
	case pkgaction.Upgrade:
		packageAction = "upgrade"
	}

	command := fmt.Sprintf("sudo -E yum %s -y %s", packageAction, name)

	if _, err := provisioner.SSHCommand(command); err != nil {
		return err
	}

	return nil
}

func (provisioner *AmazonLinuxProvisioner) installDocker() error {
	// This just to install certs to be used by Docker 1.12 - won't start the service
	if err := installDockerGeneric(provisioner, provisioner.EngineOptions.InstallURL); err != nil {
		return err
	}
	// "Manual" docker install of v1.12
	if !provisioner.installDockerToLocalAndStart() {
		log.Error("installDockerToLocalAndStart failed")
		return fmt.Errorf("installDockerToLocalAndStart failed")
	}
	return nil
	if err := provisioner.Service("docker", serviceaction.Restart); err != nil {
		return err
	}
	return nil
}

func (provisioner *AmazonLinuxProvisioner) installDockerToLocalAndStart() bool {
	cmd := "wget -O /tmp/docker-1.12.0.tgz  https://get.docker.com/builds/Linux/x86_64/docker-1.12.0.tgz"
	if out, err := provisioner.SSHCommand(cmd); err != nil {
		log.Warnf("Error downloading docker binaries: %s", err)
		log.Debugf("'%s' output:\n%s",cmd, out)
		return false
	}
	cmd = "tar -C /tmp -xvzf /tmp/docker-1.12.0.tgz"
	if out, err := provisioner.SSHCommand(cmd); err != nil {
		log.Warnf("Error unpacking docker binaries: %s", err)
		log.Debugf("'%s' output:\n%s",cmd, out)
		return false
	}
	cmd = "sudo cp /tmp/docker/* /usr/local/bin"
	if out, err := provisioner.SSHCommand(cmd); err != nil {
		log.Warnf("Error copying docker binaries to /usr/local/bin: %s", err)
		log.Debugf("'%s' output:\n%s",cmd, out)
		return false
	}
	cmd = "sudo PATH=/usr/local/bin:$PATH nohup /usr/local/bin/docker daemon -H tcp://0.0.0.0:2376 -H unix:///var/run/docker.sock --storage-driver overlay --tlsverify --tlscacert /etc/sysconfig/ca.pem --tlscert /etc/sysconfig/server.pem --tlskey /etc/sysconfig/server-key.pem --label provider=amazonec2"
	//" --cluster-store=consul://10.0.0.181:8500 --cluster-advertise=eth0:2376"
	cmd = cmd + " " + strings.Join(provisioner.EngineOptions.ArbitraryFlags," ")
	if out, err := provisioner.SSHCommand(cmd); err != nil {
		log.Warnf("Error starting docker : %s", err)
		log.Debugf("'%s' output:\n%s",cmd, out)
		return false
	}
	return true
}

func (provisioner *AmazonLinuxProvisioner) dockerDaemonResponding() bool {
	log.Debug("checking docker daemon")

	if out, err := provisioner.SSHCommand("sudo docker version"); err != nil {
		log.Warnf("Error getting SSH command to check if the daemon is up: %s", err)
		log.Debugf("'sudo docker version' output:\n%s", out)
		return false
	}

	// The daemon is up if the command worked.  Carry on.
	return true
}

func (provisioner *AmazonLinuxProvisioner) Provision(swarmOptions swarm.Options, authOptions auth.Options, engineOptions engine.Options) error {
	provisioner.SwarmOptions = swarmOptions
	provisioner.AuthOptions = authOptions
	provisioner.EngineOptions = engineOptions
	swarmOptions.Env = engineOptions.Env

	// set default storage driver for Amazon Linux
	storageDriver, err := decideStorageDriver(provisioner, "overlay", engineOptions.StorageDriver)
	if err != nil {
		return err
	}
	provisioner.EngineOptions.StorageDriver = storageDriver

	if err := provisioner.SetHostname(provisioner.Driver.GetMachineName()); err != nil {
		return err
	}

	for _, pkg := range provisioner.Packages {
		log.Debugf("installing base package: name=%s", pkg)
		if err := provisioner.Package(pkg, pkgaction.Install); err != nil {
			return err
		}
	}

	// update OS -- this is needed for libdevicemapper and the docker install
	if _, err := provisioner.SSHCommand("sudo -E yum -y update"); err != nil {
		return err
	}

	// install docker
	if err := provisioner.installDocker(); err != nil {
		return err
	}

	if err := mcnutils.WaitFor(provisioner.dockerDaemonResponding); err != nil {
		return err
	}

	if err := makeDockerOptionsDir(provisioner); err != nil {
		return err
	}

	provisioner.AuthOptions = setRemoteAuthOptions(provisioner)

	if err := ConfigureAuth(provisioner); err != nil {
		return err
	}

	if err := configureSwarm(provisioner, swarmOptions, provisioner.AuthOptions); err != nil {
		return err
	}

	return nil
}
