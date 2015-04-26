package provision

import (
	"bytes"
	"fmt"

	log "github.com/Sirupsen/logrus"
	"github.com/docker/machine/drivers"
	"github.com/docker/machine/libmachine/auth"
	"github.com/docker/machine/libmachine/provision/pkgaction"
	"github.com/docker/machine/libmachine/swarm"
	"github.com/docker/machine/ssh"
	"github.com/docker/machine/utils"
)

func init() {
	Register("Centos", &RegisteredProvisioner{
		New: NewCentosProvisioner,
	})
}

func NewCentosProvisioner(d drivers.Driver) Provisioner {
	return &CentosProvisioner{
		packages: []string{
			"curl",
		},
		Driver: d,
	}
}

type CentosProvisioner struct {
	packages      []string
	OsReleaseInfo *OsRelease
	Driver        drivers.Driver
	SwarmOptions  swarm.SwarmOptions
}

func (provisioner *CentosProvisioner) Service(name string, action pkgaction.ServiceAction) error {
	command := fmt.Sprintf("sudo service %s %s", name, action.String())

	if _, err := provisioner.SSHCommand(command); err != nil {
		return err
	}

	return nil
}

func (provisioner *CentosProvisioner) Package(name string, action pkgaction.PackageAction) error {
	var packageAction string

	switch action {
	case pkgaction.Install:
		packageAction = "install"
	case pkgaction.Remove:
		packageAction = "remove"
	}

	command := fmt.Sprintf("sudo -E yum -y %s %s", packageAction, name)

	if _, err := provisioner.SSHCommand(command); err != nil {
		return err
	}

	return nil
}

func (provisioner *CentosProvisioner) dockerDaemonResponding() bool {
	if _, err := provisioner.SSHCommand("sudo docker version"); err != nil {
		log.Warn("Error getting SSH command to check if the daemon is up: %s", err)
		return false
	}

	// The daemon is up if the command worked.  Carry on.
	return true
}

func (provisioner *CentosProvisioner) Provision(swarmOptions swarm.SwarmOptions, authOptions auth.AuthOptions) error {
	// fix centos sudo config related to this bug: https://bugzilla.redhat.com/show_bug.cgi?id=1020147
	if _, err := provisioner.SSHCommand("-t sed -i 's/^Defaults.*requiretty$/#\\ commented\\ out\\ by\\ docker\\ machine\\n#Defaults\\ \\ \\ \\ requiretty/g' /etc/sudoers"); err != nil {
		return err
	}

	if err := provisioner.SetHostname(provisioner.Driver.GetMachineName()); err != nil {
		return err
	}

  // yum update or docker will be broken
	if _, err := provisioner.SSHCommand("yum -y update"); err != nil {
		return err
	}

	for _, pkg := range provisioner.packages {
		if err := provisioner.Package(pkg, pkgaction.Install); err != nil {
			return err
		}
	}

	// configure firewalld
	if _, err := provisioner.SSHCommand("printf '<?xml version=\"1.0\" encoding=\"utf-8\"?>\n<service>\n  <short>Docker Daemon</short>\n  <port protocol=\"tcp\" port=\"2376\"/>\n</service>\n' > /etc/firewalld/services/docker.xml && sed -i 's/<\\/zone>/  <service name=\\\"docker\\\"\\/>\\n<\\/zone>/g' /etc/firewalld/zones/public.xml && service firewalld restart"); err != nil {
		return err
	}

	if err := installDockerGeneric(provisioner); err != nil {
		return err
	}

	if err := utils.WaitFor(provisioner.dockerDaemonResponding); err != nil {
		return err
	}

	if err := ConfigureAuth(provisioner, authOptions); err != nil {
		return err
	}

	if err := configureSwarm(provisioner, swarmOptions); err != nil {
		return err
	}

	return nil
}

func (provisioner *CentosProvisioner) Hostname() (string, error) {
	output, err := provisioner.SSHCommand("hostname")
	if err != nil {
		return "", err
	}

	var so bytes.Buffer
	if _, err := so.ReadFrom(output.Stdout); err != nil {
		return "", err
	}

	return so.String(), nil
}

func (provisioner *CentosProvisioner) SetHostname(hostname string) error {
	if _, err := provisioner.SSHCommand(fmt.Sprintf(
		"sudo hostname %s && echo %q | sudo tee /etc/hostname",
		hostname,
		hostname,
	)); err != nil {
		return err
	}

	if _, err := provisioner.SSHCommand(fmt.Sprintf(
		"sudo sed -i '/^127.0.0.1/ s/$/ %s/' /etc/hosts",
		hostname,
	)); err != nil {
		return err
	}

	return nil
}

func (provisioner *CentosProvisioner) GetDockerOptionsDir() string {
	return "/etc/default/docker"
}

func (provisioner *CentosProvisioner) SSHCommand(args string) (ssh.Output, error) {
	return drivers.RunSSHCommandFromDriver(provisioner.Driver, args)
}

func (provisioner *CentosProvisioner) CompatibleWithHost() bool {
	return provisioner.OsReleaseInfo.Id == "centos"
}

func (provisioner *CentosProvisioner) SetOsReleaseInfo(info *OsRelease) {
	provisioner.OsReleaseInfo = info
}

func (provisioner *CentosProvisioner) GenerateDockerOptions(dockerPort int, authOptions auth.AuthOptions) (*DockerOptions, error) {
	defaultDaemonOpts := getDefaultDaemonOpts(provisioner.Driver.DriverName(), authOptions)
	daemonOpts := fmt.Sprintf("--host=unix:///var/run/docker.sock --host=tcp://0.0.0.0:%d", dockerPort)
	daemonOptsDir := "/etc/sysconfig/docker"
	daemonCfg := fmt.Sprintf("OPTIONS='%s %s'", defaultDaemonOpts, daemonOpts)
	return &DockerOptions{
		EngineOptions:     daemonCfg,
		EngineOptionsPath: daemonOptsDir,
	}, nil
}

func (provisioner *CentosProvisioner) GetDriver() drivers.Driver {
	return provisioner.Driver
}
