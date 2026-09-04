package xregistry

// The served subset of the xRegistry 1.0-rc2x document model, decoded from
// GET /export. The registry is API-only and read-only (the service refuses
// filtering, inlining, and pagination), so the whole model arrives in one
// document and all resolution joins happen in memory against it.
//
// /export is the doc view: references are root-relative xids, collection
// urls and counts are absent, and for hasdocument resources the default
// Version's attributes are NOT repeated on the Resource. For messages —
// maxversions: 1 — every attribute therefore lives on the single implicit
// version, never on the message resource itself. Schemas, by contrast, are
// multi-version resources whose versions each carry isdefault.

// Export is the whole registry document from GET /export. The three
// collections are top-level group collections; an absent collection decodes
// as an empty map, so a caller never nil-checks.
type Export struct {
	SpecVersion   string                  `json:"specversion"`
	RegistryID    string                  `json:"registryid"`
	MessageGroups map[string]MessageGroup `json:"messagegroups"`
	Endpoints     map[string]Endpoint     `json:"endpoints"`
	SchemaGroups  map[string]SchemaGroup  `json:"schemagroups"`
}

// MessageGroup is one xRegistry message group carrying its message
// collection. The registry guarantees every published group is referenced
// by at least one channelled endpoint.
type MessageGroup struct {
	ID       string             `json:"messagegroupid"`
	Name     string             `json:"name,omitempty"`
	Messages map[string]Message `json:"messages"`
}

// Message is one message definition in the doc view. Definition attributes
// (envelope metadata, dataschemaxid) live on the single implicit version
// under Versions, not here — this is the doc view, which does not repeat
// the default version's attributes on the resource.
type Message struct {
	ID       string                     `json:"messageid"`
	Versions map[string]*MessageVersion `json:"versions"`

	// Definition fields are set only on per-entity (API view) reads, where
	// the default version's attributes ARE repeated on the resource. Code
	// reading a message goes through defaultMessage() so the two views
	// resolve identically.
	Envelope         string                 `json:"envelope,omitempty"`
	EnvelopeMetadata map[string]CEAttribute `json:"envelopemetadata,omitempty"`
	DataSchemaFormat string                 `json:"dataschemaformat,omitempty"`
	DataSchemaXID    string                 `json:"dataschemaxid,omitempty"`
	DataContentType  string                 `json:"datacontenttype,omitempty"`
}

// MessageVersion is one version of a message definition. Messages have
// exactly one implicit version (versionid "1") carrying the definition.
type MessageVersion struct {
	VersionID        string                 `json:"versionid"`
	IsDefault        bool                   `json:"isdefault"`
	Envelope         string                 `json:"envelope,omitempty"`
	EnvelopeMetadata map[string]CEAttribute `json:"envelopemetadata,omitempty"`
	DataSchemaFormat string                 `json:"dataschemaformat,omitempty"`
	DataSchemaXID    string                 `json:"dataschemaxid,omitempty"`
	DataContentType  string                 `json:"datacontenttype,omitempty"`
}

// CEAttribute is one envelope metadata attribute. The registry constrains
// some attributes with a constant value; a message's CloudEvents type is
// the value of its `type` attribute.
type CEAttribute struct {
	Value       any    `json:"value"`
	Type        string `json:"type,omitempty"`
	Required    bool   `json:"required,omitempty"`
	Description string `json:"description,omitempty"`
}

// Endpoint is one registry endpoint: a role, the channel the endpoint
// speaks on, and the message groups allowed on it. Channel is
// <pubsub>/<topic>; the split helper in resolve.go owns the format.
type Endpoint struct {
	ID            string   `json:"endpointid"`
	Name          string   `json:"name,omitempty"`
	Usage         []string `json:"usage,omitempty"`
	Channel       string   `json:"channel,omitempty"`
	MessageGroups []string `json:"messagegroups,omitempty"`
}

// SchemaGroup is one schema group carrying schema resources.
type SchemaGroup struct {
	ID      string            `json:"schemagroupid"`
	Name    string            `json:"name,omitempty"`
	Schemas map[string]Schema `json:"schemas"`
}

// Schema is one schema resource. Versions carry isdefault; the default
// version URL is the immutable pin a CloudEvent dataschema references.
type Schema struct {
	ID       string             `json:"schemaid"`
	Format   string             `json:"format,omitempty"`
	Versions map[string]Version `json:"versions"`
}

// Version is one immutable schema version. XID is root-relative; the
// pinned URL is the base URL joined with it.
type Version struct {
	VersionID string `json:"versionid"`
	XID       string `json:"xid,omitempty"`
	IsDefault bool   `json:"isdefault"`
}
