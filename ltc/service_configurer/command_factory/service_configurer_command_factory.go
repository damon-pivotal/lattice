package command_factory

import (
	"errors"
	"fmt"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/cloudfoundry-incubator/bbs/models"
	"github.com/cloudfoundry-incubator/lattice/ltc/app_runner"
	"github.com/cloudfoundry-incubator/lattice/ltc/app_runner/command_factory"
	"github.com/cloudfoundry-incubator/lattice/ltc/config/dav_blob_store"
	"github.com/cloudfoundry-incubator/lattice/ltc/docker_runner/docker_metadata_fetcher"
	"github.com/cloudfoundry-incubator/lattice/ltc/docker_runner/docker_repository_name_formatter"
	"github.com/cloudfoundry-incubator/lattice/ltc/exit_handler/exit_codes"
	"github.com/codegangsta/cli"
)

type ServiceConfigurerCommandFactory struct {
	command_factory.AppRunnerCommandFactory

	BlobStore             *dav_blob_store.BlobStore
	DockerMetadataFetcher docker_metadata_fetcher.DockerMetadataFetcher
}

func (factory *ServiceConfigurerCommandFactory) MakeListServicesCommand() cli.Command {
	return cli.Command{
		Name:        "list-services",
		Aliases:     []string{"lss"},
		Usage:       "Lists services",
		Description: "ltc list-services",
		Action:      factory.listServices,
	}
}

func (factory *ServiceConfigurerCommandFactory) listServices(context *cli.Context) {
	serviceBlobs, err := factory.BlobStore.List()
	if err != nil {
		panic(err)
	}

	for _, service := range serviceBlobs {
		if strings.HasPrefix(service.Path, "services/") {
			p := path.Base(service.Path)
			factory.UI.SayLine(p[:len(p)-5])
		}
	}
}

func (factory *ServiceConfigurerCommandFactory) MakeBindServiceCommand() cli.Command {
	return cli.Command{
		Name:        "bind-service",
		Aliases:     []string{"bs"},
		Usage:       "Binds a future app name to an existing service",
		Description: "ltc bind-service APP_NAME SERVICE_NAME",
		Action:      factory.bindService,
	}
}

func (factory *ServiceConfigurerCommandFactory) bindService(context *cli.Context) {
	appName := context.Args().Get(0)
	serviceName := context.Args().Get(1)

	if len(context.Args()) < 2 {
		factory.UI.SayIncorrectUsage("APP_NAME and SERVICE_NAME are required")
		factory.ExitHandler.Exit(exit_codes.InvalidSyntax)
		return
	}

	err := factory.BlobStore.Upload("bindings/"+appName+"-"+serviceName, strings.NewReader(""))
	if err != nil {
		panic(err)
	}

	factory.UI.Say("Bound " + appName + " to service " + serviceName + ".")
}

func (factory *ServiceConfigurerCommandFactory) MakeRemoveServiceCommand() cli.Command {
	return cli.Command{
		Name:        "remove-service",
		Aliases:     []string{"rs"},
		Usage:       "Removes a service",
		Description: "ltc remove-service SERVICE_NAME",
		Action:      factory.removeService,
	}
}

func (factory *ServiceConfigurerCommandFactory) removeService(context *cli.Context) {
	appName := context.Args().First()

	factory.UI.SayLine(fmt.Sprintf("Removing %s...", appName))
	err := factory.AppRunner.RemoveApp(appName)
	if err != nil {
		panic(err)
	}

	err = factory.BlobStore.Delete("services/" + appName + ".json")
	if err != nil {
		panic(err)
	}
}

func (factory *ServiceConfigurerCommandFactory) MakeCreateServiceCommand() cli.Command {
	var createFlags = []cli.Flag{
		cli.StringFlag{
			Name:  "working-dir, w",
			Usage: "Working directory for container (overrides Docker metadata)",
			Value: "",
		},
		cli.BoolFlag{
			Name:  "run-as-root, r",
			Usage: "Runs in the context of the root user",
		},
		cli.StringSliceFlag{
			Name:  "env, e",
			Usage: "Environment variables (can be passed multiple times)",
			Value: &cli.StringSlice{},
		},
		cli.IntFlag{
			Name:  "cpu-weight, c",
			Usage: "Relative CPU weight for the container (valid values: 1-100)",
			Value: 100,
		},
		cli.IntFlag{
			Name:  "memory-mb, m",
			Usage: "Memory limit for container in MB",
			Value: 128,
		},
		cli.IntFlag{
			Name:  "disk-mb, d",
			Usage: "Disk limit for container in MB",
			Value: 0,
		},
		cli.StringFlag{
			Name:  "ports, p",
			Usage: "Ports to expose on the container (comma delimited)",
		},
		cli.IntFlag{
			Name:  "monitor-port, M",
			Usage: "Selects the port used to healthcheck the app",
		},
		cli.StringFlag{
			Name: "monitor-url, U",
			Usage: "Uses HTTP to healthcheck the app\n\t\t" +
				"format is: port:/path/to/endpoint",
		},
		cli.DurationFlag{
			Name:  "monitor-timeout",
			Usage: "Timeout for the app healthcheck",
			Value: time.Second,
		},
		cli.StringFlag{
			Name: "routes, R",
			Usage: "Route mappings to exposed ports as follows:\n\t\t" +
				"--routes=80:web,8080:api will route web to 80 and api to 8080",
		},
		cli.IntFlag{
			Name:  "instances, i",
			Usage: "Number of application instances to spawn on launch",
			Value: 1,
		},
		cli.BoolFlag{
			Name:  "no-monitor",
			Usage: "Disables healthchecking for the app",
		},
		cli.BoolFlag{
			Name:  "no-routes",
			Usage: "Registers no routes for the app",
		},
		cli.DurationFlag{
			Name:  "timeout, t",
			Usage: "Polling timeout for app to start",
			Value: command_factory.DefaultPollingTimeout,
		},
	}

	var createServiceCommand = cli.Command{
		Name:        "create-service",
		Aliases:     []string{"cs"},
		Usage:       "Creates a service",
		Description: "ltc create-service SERVICE_NAME DOCKER_IMAGE USER PASS",
		Action:      factory.createService,
		Flags:       createFlags,
	}

	return createServiceCommand
}

func (factory *ServiceConfigurerCommandFactory) createService(context *cli.Context) {
	workingDirFlag := context.String("working-dir")
	envVarsFlag := context.StringSlice("env")
	instancesFlag := context.Int("instances")
	cpuWeightFlag := uint(context.Int("cpu-weight"))
	memoryMBFlag := context.Int("memory-mb")
	diskMBFlag := context.Int("disk-mb")
	portsFlag := context.String("ports")
	runAsRootFlag := context.Bool("run-as-root")
	noMonitorFlag := context.Bool("no-monitor")
	portMonitorFlag := context.Int("monitor-port")
	urlMonitorFlag := context.String("monitor-url")
	monitorTimeoutFlag := context.Duration("monitor-timeout")
	routesFlag := context.String("routes")
	noRoutesFlag := context.Bool("no-routes")
	timeoutFlag := context.Duration("timeout")
	serviceName := context.Args().Get(0)
	dockerPath := context.Args().Get(1)
	serviceUser := context.Args().Get(2)
	servicePass := context.Args().Get(3)
	terminator := context.Args().Get(4)
	startCommand := context.Args().Get(5)

	var appArgs []string

	switch {
	case len(context.Args()) < 4:
		factory.UI.SayIncorrectUsage("SERVICE_NAME, DOCKER_IMAGE, USER and PASS are required")
		factory.ExitHandler.Exit(exit_codes.InvalidSyntax)
		return
	case startCommand != "" && terminator != "--":
		factory.UI.SayIncorrectUsage("'--' Required before start command")
		factory.ExitHandler.Exit(exit_codes.InvalidSyntax)
		return
	case len(context.Args()) > 6:
		appArgs = context.Args()[6:]
	case cpuWeightFlag < 1 || cpuWeightFlag > 100:
		factory.UI.SayIncorrectUsage("Invalid CPU Weight")
		factory.ExitHandler.Exit(exit_codes.InvalidSyntax)
		return
	}

	imageMetadata, err := factory.DockerMetadataFetcher.FetchMetadata(dockerPath)
	if err != nil {
		factory.UI.Say(fmt.Sprintf("Error fetching image metadata: %s", err))
		factory.ExitHandler.Exit(exit_codes.BadDocker)
		return
	}

	exposedPorts, err := factory.getExposedPortsFromArgs(portsFlag, imageMetadata)
	if err != nil {
		factory.UI.Say(err.Error())
		factory.ExitHandler.Exit(exit_codes.InvalidSyntax)
		return
	}

	monitorConfig, err := factory.GetMonitorConfig(exposedPorts, portMonitorFlag, noMonitorFlag, urlMonitorFlag, monitorTimeoutFlag)
	if err != nil {
		factory.UI.Say(err.Error())
		if err.Error() == command_factory.MonitorPortNotExposed {
			factory.ExitHandler.Exit(exit_codes.CommandFailed)
		} else {
			factory.ExitHandler.Exit(exit_codes.InvalidSyntax)
		}
		return
	}

	if workingDirFlag == "" {
		factory.UI.Say("No working directory specified, using working directory from the image metadata...\n")
		if imageMetadata.WorkingDir != "" {
			workingDirFlag = imageMetadata.WorkingDir
			factory.UI.Say("Working directory is:\n")
			factory.UI.Say(workingDirFlag + "\n")
		} else {
			workingDirFlag = "/"
		}
	}

	if !noMonitorFlag {
		factory.UI.Say(fmt.Sprintf("Monitoring the app on port %d...\n", monitorConfig.Port))
	} else {
		factory.UI.Say("No ports will be monitored.\n")
	}

	if startCommand == "" {
		if len(imageMetadata.StartCommand) == 0 {
			factory.UI.SayLine("Unable to determine start command from image metadata.")
			factory.ExitHandler.Exit(exit_codes.BadDocker)
			return
		}

		factory.UI.Say("No start command specified, using start command from the image metadata...\n")
		startCommand = imageMetadata.StartCommand[0]

		factory.UI.Say("Start command is:\n")
		factory.UI.Say(strings.Join(imageMetadata.StartCommand, " ") + "\n")

		appArgs = imageMetadata.StartCommand[1:]
	}

	routeOverrides, err := factory.ParseRouteOverrides(routesFlag)
	if err != nil {
		factory.UI.Say(err.Error())
		factory.ExitHandler.Exit(exit_codes.InvalidSyntax)
		return
	}

	rootFS, err := docker_repository_name_formatter.FormatForReceptor(dockerPath)
	if err != nil {
		factory.UI.Say(err.Error())
		factory.ExitHandler.Exit(exit_codes.CommandFailed)
		return
	}

	env := factory.BuildAppEnvironment(envVarsFlag, serviceName)
	populateEnvForService(serviceName, serviceUser, servicePass, env)

	err = factory.AppRunner.CreateApp(app_runner.CreateAppParams{
		AppEnvironmentParams: app_runner.AppEnvironmentParams{
			EnvironmentVariables: env,
			Privileged:           runAsRootFlag,
			Monitor:              monitorConfig,
			Instances:            instancesFlag,
			CPUWeight:            cpuWeightFlag,
			MemoryMB:             memoryMBFlag,
			DiskMB:               diskMBFlag,
			ExposedPorts:         exposedPorts,
			WorkingDir:           workingDirFlag,
			RouteOverrides:       routeOverrides,
			NoRoutes:             noRoutesFlag,
		},

		Name:         serviceName,
		RootFS:       rootFS,
		StartCommand: startCommand,
		AppArgs:      appArgs,
		Timeout:      timeoutFlag,

		Setup: &models.DownloadAction{
			From: "http://file_server.service.dc1.consul:8080/v1/static/healthcheck.tgz",
			To:   "/tmp",
			User: "vcap",
		},
	})
	if err != nil {
		factory.UI.Say(fmt.Sprintf("Error creating app: %s", err))
		factory.ExitHandler.Exit(exit_codes.CommandFailed)
		return
	}

	factory.WaitForAppCreation(serviceName, timeoutFlag, instancesFlag, noRoutesFlag, routeOverrides)

	factory.UI.SayLine("Service " + serviceName + " running.")

	appInfo, err := factory.AppExaminer.AppStatus(serviceName)
	if err != nil {
		panic(err)
	}

	vcapJSON := makeVCAPServiceJSON(
		serviceName,
		serviceUser,
		servicePass,
		appInfo.ActualInstances[0].Ip,
		appInfo.ActualInstances[0].Ports[0].HostPort,
	)
	err = factory.BlobStore.Upload("services/"+serviceName+".json", strings.NewReader(vcapJSON))
	if err != nil {
		panic(err)
	}

	factory.UI.Say("Service " + serviceName + " registered.")
}

func populateEnvForService(serviceType, serviceUser, servicePass string, env map[string]string) {
	switch serviceType {
	case "postgres":
		env["POSTGRES_USER"] = serviceUser
		env["POSTGRES_PASSWORD"] = servicePass
	case "mysql":
		env["MYSQL_USER"] = serviceUser
		env["MYSQL_DATABASE"] = serviceUser
		env["MYSQL_PASSWORD"] = servicePass
		env["MYSQL_ROOT_PASSWORD"] = servicePass
	default:
		panic("unknown service type " + serviceType)
	}
}

func makeVCAPServiceJSON(serviceType, serviceUser, servicePass, serviceIP string, servicePort uint16) string {
	switch serviceType {
	case "postgres":
		return fmt.Sprintf(`{"postgresql":[{"credentials":{
			"url": "postgres://%s:%s@%s:%d/%s"
		}}]}`, serviceUser, servicePass, serviceIP, servicePort, serviceUser)
	case "mysql":
		return fmt.Sprintf(`{"mysql":[{"credentials":{
			"url": "mysql://%s:%s@%s:%d/%s"
		}}]}`, serviceUser, servicePass, serviceIP, servicePort, serviceUser)
	default:
		panic("unknown service type " + serviceType)
	}
}

func (factory *ServiceConfigurerCommandFactory) getExposedPortsFromArgs(portsFlag string, imageMetadata *docker_metadata_fetcher.ImageMetadata) ([]uint16, error) {
	if portsFlag != "" {
		portStrings := strings.Split(portsFlag, ",")
		sort.Strings(portStrings)

		convertedPorts := []uint16{}
		for _, p := range portStrings {
			intPort, err := strconv.Atoi(p)
			if err != nil || intPort > 65535 {
				return []uint16{}, errors.New(command_factory.InvalidPortErrorMessage)
			}
			convertedPorts = append(convertedPorts, uint16(intPort))
		}
		return convertedPorts, nil
	}

	if len(imageMetadata.ExposedPorts) > 0 {
		var exposedPortStrings []string
		for _, port := range imageMetadata.ExposedPorts {
			exposedPortStrings = append(exposedPortStrings, strconv.Itoa(int(port)))
		}
		factory.UI.Say(fmt.Sprintf("No port specified, using exposed ports from the image metadata.\n\tExposed Ports: %s\n", strings.Join(exposedPortStrings, ", ")))
		return imageMetadata.ExposedPorts, nil
	}

	factory.UI.Say(fmt.Sprintf("No port specified, image metadata did not contain exposed ports. Defaulting to 8080.\n"))
	return []uint16{8080}, nil
}
