package schema

// The fixture types cover what reflection must handle: patterns, enums,
// ranges, optional fields, nested and repeated structs, an embedded struct
// and secret fields at several depths.

type fixtureAddress struct {
	Host string `json:"host" jsonschema:"pattern=^[a-z0-9.-]+$"`
	Port int    `json:"port" jsonschema:"minimum=1,maximum=65535"`
}

type fixtureCredential struct {
	User string `json:"user"`
	Key  string `json:"key" secret:"true"`
}

type fixtureAuth struct {
	APIKey string `json:"apiKey,omitempty" secret:"true"`
}

type fixtureInput struct {
	fixtureAuth
	Name     string              `json:"name" jsonschema:"pattern=^[a-z][a-z0-9-]*$"`
	Mode     string              `json:"mode" jsonschema:"enum=fast,enum=safe"`
	Count    int                 `json:"count,omitempty" jsonschema:"minimum=1,maximum=10"`
	Password string              `json:"password" secret:"true"`
	Target   fixtureAddress      `json:"target"`
	Backups  []fixtureAddress    `json:"backups,omitempty"`
	Creds    []fixtureCredential `json:"creds,omitempty"`
	Tags     []string            `json:"tags,omitempty"`
}

type fixtureOutput struct {
	ID     string         `json:"id"`
	Token  string         `json:"token" secret:"true"`
	Target fixtureAddress `json:"target"`
}
