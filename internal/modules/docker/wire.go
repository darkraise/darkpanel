package docker

import "time"

// Engine is the Docker daemon's availability and counts.
type Engine struct {
	Available  bool   `json:"available"`
	Reason     string `json:"reason,omitempty"`
	Version    string `json:"version,omitempty"`
	APIVersion string `json:"apiVersion,omitempty"`
	OS         string `json:"os,omitempty"`
	Arch       string `json:"arch,omitempty"`
	Containers int    `json:"containers"`
	Running    int    `json:"running"`
	Images     int    `json:"images"`
}

// Port is one published or exposed port of a container.
type Port struct {
	IP          string `json:"ip,omitempty"`
	PrivatePort uint16 `json:"privatePort"`
	PublicPort  uint16 `json:"publicPort,omitempty"`
	Proto       string `json:"proto"`
}

// Usage is a running container's resource use from a one-shot stats read.
type Usage struct {
	CPUPercent float64 `json:"cpuPercent"`
	MemUsage   uint64  `json:"memUsage"`
	MemLimit   uint64  `json:"memLimit"`
}

// Container is one container as the container list shows it.
type Container struct {
	ID      string            `json:"id"`
	Name    string            `json:"name"` // without Docker's leading "/"
	Image   string            `json:"image"`
	ImageID string            `json:"imageId"`
	State   string            `json:"state"` // Docker's State
	Status  string            `json:"status"`
	Created time.Time         `json:"created"`
	Ports   []Port            `json:"ports"`
	Labels  map[string]string `json:"labels,omitempty"`
	Project string            `json:"project,omitempty"` // com.docker.compose.project
	Service string            `json:"service,omitempty"` // com.docker.compose.service
	Owner   string            `json:"owner,omitempty"`   // darkpanel.module
	Usage   *Usage            `json:"usage,omitempty"`   // running containers; nil when the one-shot stats read failed
}

// ContainerList is the output of the containers resource.
type ContainerList struct {
	Containers []Container `json:"containers"`
}

// Image is one image as the image list shows it.
type Image struct {
	ID         string    `json:"id"`
	Tags       []string  `json:"tags"`
	Digests    []string  `json:"digests"`
	Size       int64     `json:"size"`
	Created    time.Time `json:"created"`
	Containers []string  `json:"containers"` // names of containers using it
	Dangling   bool      `json:"dangling"`
}

// ImageList is the output of the images resource.
type ImageList struct {
	Images []Image `json:"images"`
}

// Network is one network as the network list shows it.
type Network struct {
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	Driver     string    `json:"driver"`
	Scope      string    `json:"scope"`
	Internal   bool      `json:"internal"`
	IPv6       bool      `json:"ipv6"`
	Subnets    []string  `json:"subnets"`
	Containers []string  `json:"containers"` // names
	Owner      string    `json:"owner,omitempty"`
	Created    time.Time `json:"created"`
}

// NetworkList is the output of the networks resource.
type NetworkList struct {
	Networks []Network `json:"networks"`
}

// Volume is one volume as the volume list shows it.
type Volume struct {
	Name       string            `json:"name"`
	Driver     string            `json:"driver"`
	Mountpoint string            `json:"mountpoint"`
	Created    time.Time         `json:"created"`
	Labels     map[string]string `json:"labels,omitempty"`
	Containers []string          `json:"containers"` // names
	Owner      string            `json:"owner,omitempty"`
}

// VolumeList is the output of the volumes resource.
type VolumeList struct {
	Volumes []Volume `json:"volumes"`
}

// Project is one Compose project the module knows.
type Project struct {
	Name        string     `json:"name"`
	Directory   string     `json:"directory"`
	Files       []string   `json:"files"`
	EnvFiles    []string   `json:"envFiles"`
	Profiles    []string   `json:"profiles"`
	ConfigHash  string     `json:"configHash"`  // "" when the files cannot be read
	HashChanged bool       `json:"hashChanged"` // differs from the hash of the last run
	Containers  []string   `json:"containers"`  // names, current
	Running     int        `json:"running"`
	FirstSeen   time.Time  `json:"firstSeen"`
	LastRun     *time.Time `json:"lastRun,omitempty"`
}

// ProjectList is the output of the projects resource.
type ProjectList struct {
	Projects []Project `json:"projects"`
}

// ContainerInput names the container an action acts on, by id or name.
type ContainerInput struct {
	Container string `json:"container"`
} // id or name
// ContainerState is a container's identity and whether it runs.
type ContainerState struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Running bool   `json:"running"`
}

// ContainerRemoveInput is the input of the container removal action.
type ContainerRemoveInput struct {
	Container string `json:"container"`
	Force     bool   `json:"force,omitempty"`
	Volumes   bool   `json:"volumes,omitempty"`
}

// Removed names an object an action removed.
type Removed struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// EnvVar is one environment variable of a container.
type EnvVar struct {
	Name  string `json:"name"`
	Value string `json:"value"` // "[redacted]" in ContainerDetail
}

// Mount is one mount of a container.
type Mount struct {
	Type        string `json:"type"`
	Name        string `json:"name,omitempty"`
	Source      string `json:"source"`
	Destination string `json:"destination"`
	RW          bool   `json:"rw"`
}

// EndpointView is a container's endpoint on one network.
type EndpointView struct {
	Network    string   `json:"network"` // name
	NetworkID  string   `json:"networkId"`
	Container  string   `json:"container"` // name
	EndpointID string   `json:"endpointId,omitempty"`
	Aliases    []string `json:"aliases,omitempty"`
	IPv4       string   `json:"ipv4,omitempty"`
	IPv6       string   `json:"ipv6,omitempty"`
}

// ContainerDetail is the output of the container inspect action.
type ContainerDetail struct {
	ID            string            `json:"id"`
	Name          string            `json:"name"`
	Image         string            `json:"image"`
	ImageID       string            `json:"imageId"`
	Created       time.Time         `json:"created"`
	State         string            `json:"state"`
	Running       bool              `json:"running"`
	StartedAt     string            `json:"startedAt,omitempty"`
	FinishedAt    string            `json:"finishedAt,omitempty"`
	ExitCode      int               `json:"exitCode"`
	RestartPolicy string            `json:"restartPolicy,omitempty"`
	Cmd           []string          `json:"cmd,omitempty"`
	Entrypoint    []string          `json:"entrypoint,omitempty"`
	WorkingDir    string            `json:"workingDir,omitempty"`
	User          string            `json:"user,omitempty"`
	Env           []EnvVar          `json:"env"` // values redacted for every container
	Labels        map[string]string `json:"labels,omitempty"`
	Ports         []Port            `json:"ports"` // published bindings: ip, publicPort, privatePort, proto
	Mounts        []Mount           `json:"mounts"`
	Networks      []EndpointView    `json:"networks"`
	Project       string            `json:"project,omitempty"`
	Service       string            `json:"service,omitempty"`
	Owner         string            `json:"owner,omitempty"`
}

// LogsInput is the input of the container logs action.
type LogsInput struct {
	Container  string `json:"container"`
	Tail       int    `json:"tail,omitempty"`  // 1–5000, default 200
	Since      string `json:"since,omitempty"` // RFC 3339
	Timestamps bool   `json:"timestamps,omitempty"`
}

// LogLine is one line of a container's log output.
type LogLine struct {
	Stream string `json:"stream"` // stdout | stderr
	Text   string `json:"text"`
}

// LogsOutput is the output of the container logs action.
type LogsOutput struct {
	Lines []LogLine `json:"lines"`
}

// RevealedEnv is a container's environment with its values in the clear.
type RevealedEnv struct {
	Env []EnvVar `json:"env" secret:"true"`
}

// ImagePullInput is the input of the image pull action.
type ImagePullInput struct {
	Image string `json:"image"`
}

// PulledImage is the image a pull produced.
type PulledImage struct {
	Image   string   `json:"image"`
	ID      string   `json:"id"`
	Digests []string `json:"digests"`
}

// ImageRemoveInput is the input of the image removal action.
type ImageRemoveInput struct {
	Image string `json:"image"`
	Force bool   `json:"force,omitempty"`
}

// RemovedImage lists the tags removed and the images deleted by an image removal.
type RemovedImage struct {
	Untagged []string `json:"untagged"`
	Deleted  []string `json:"deleted"`
}

// ImagePruneInput is the input of the image prune action, which takes none.
type ImagePruneInput struct{}

// PrunedImage is one dangling image a prune deleted.
type PrunedImage struct {
	ID   string   `json:"id"`
	Tags []string `json:"tags"`
	Size int64    `json:"size"`
}

// Pruned is the output of the image prune action.
type Pruned struct {
	Images    []PrunedImage `json:"images"`
	Reclaimed int64         `json:"reclaimed"`
}

// NetworkCreateInput is the input of the network creation action.
type NetworkCreateInput struct {
	Name       string            `json:"name"`
	Driver     string            `json:"driver,omitempty"` // default bridge
	Internal   bool              `json:"internal,omitempty"`
	Attachable bool              `json:"attachable,omitempty"`
	IPv6       bool              `json:"ipv6,omitempty"`
	Subnet     string            `json:"subnet,omitempty"`
	Gateway    string            `json:"gateway,omitempty"`
	Labels     map[string]string `json:"labels,omitempty"` // never darkpanel.module
}

// VolumeCreateInput is the input of the volume creation action.
type VolumeCreateInput struct {
	Name   string            `json:"name"`
	Driver string            `json:"driver,omitempty"` // default local
	Labels map[string]string `json:"labels,omitempty"` // never darkpanel.module
}

// Created names the network or volume an action created.
type Created struct {
	ID   string `json:"id"` // a volume's name
	Name string `json:"name"`
}

// NetworkInput names the network an action removes.
type NetworkInput struct {
	Network string `json:"network"`
}

// VolumeInput names the volume an action removes.
type VolumeInput struct {
	Volume string `json:"volume"`
}

// ConnectInput is the input of the network connect action.
type ConnectInput struct {
	Network   string   `json:"network"`
	Container string   `json:"container"`
	Aliases   []string `json:"aliases,omitempty"`
	IPv4      string   `json:"ipv4,omitempty"`
	IPv6      string   `json:"ipv6,omitempty"`
}

// DisconnectInput is the input of the network disconnect action.
type DisconnectInput struct {
	Network   string `json:"network"`
	Container string `json:"container"`
	Force     bool   `json:"force,omitempty"`
}

// ComposeInput is the input of the Compose actions.
type ComposeInput struct {
	Project  string    `json:"project"`
	Profiles *[]string `json:"profiles,omitempty"` // nil: the stored list; given: replaces it at apply
}

// ComposeResult is the output of the Compose actions.
type ComposeResult struct {
	Project    string           `json:"project"`
	Containers []ContainerState `json:"containers"`
}

// LogsParams are the parameters of the logs stream.
type LogsParams struct {
	Container string `json:"container"`
	Follow    bool   `json:"follow,omitempty"`
	Since     string `json:"since,omitempty"` // RFC 3339
	Tail      string `json:"tail,omitempty"`  // "all" or a number, default "200"
}

// LogEvent is one event of the logs stream.
type LogEvent struct {
	Stream string `json:"stream"`
	Text   string `json:"text"`
}

// StatsParams are the parameters of the stats stream.
type StatsParams struct {
	Container string `json:"container"`
}

// StatsEvent is one event of the stats stream.
type StatsEvent struct {
	Read       string  `json:"read"`
	CPUPercent float64 `json:"cpuPercent"`
	MemUsage   uint64  `json:"memUsage"`
	MemLimit   uint64  `json:"memLimit"`
	NetRx      uint64  `json:"netRx"`
	NetTx      uint64  `json:"netTx"`
	Pids       uint64  `json:"pids"`
}

// PullParams are the parameters of the pull stream.
type PullParams struct {
	ChangeID string `json:"changeId"`
}

// PullEvent is one event of the pull stream.
type PullEvent struct {
	Status   string `json:"status"`
	ID       string `json:"id,omitempty"`
	Progress string `json:"progress,omitempty"`
	Current  int64  `json:"current,omitempty"`
	Total    int64  `json:"total,omitempty"`
	Error    string `json:"error,omitempty"`
}
