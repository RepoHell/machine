package provision

import (
	"github.com/docker/machine/libmachine/drivers"
)

func init() {
	Register("AmazonLinux", &RegisteredProvisioner{
		New: NewAmazonLinuxProvisioner,
	})
}

func NewAmazonLinuxProvisioner(d drivers.Driver) Provisioner {
	return &UbuntuProvisioner{
		GenericProvisioner{
			SSHCommander:      GenericSSHCommander{Driver: d},
			DockerOptionsDir:  "/etc/docker",
			DaemonOptionsFile: "/etc/default/docker",
			OsReleaseID:       "amzn",
			Packages: []string{
				"curl",
			},
			Driver: d,
		},
	}
}

type AmazonLinuxProvisioner struct {
	*UbuntuProvisioner
}

func (provisioner *AmazonLinuxProvisioner) String() string {
	return "amzn"
}

func (provisioner *AmazonLinuxProvisioner) CompatibleWithHost() bool {
	isAmazon := provisioner.OsReleaseInfo.ID == provisioner.OsReleaseID
	if !isAmazon {
		return false
	}
	return true
}

