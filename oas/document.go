package oas

import (
	"encoding/json"
	"iter"
)

// Document is the root of an OpenAPI 3.2 description. Older 3.x documents
// are upgraded into this model by the loader, so consumers only handle 3.2.
type Document struct {
	OpenAPI           string                 `json:"openapi"`
	Self              string                 `json:"$self,omitempty"`
	Info              *Info                  `json:"info"`
	JSONSchemaDialect string                 `json:"jsonSchemaDialect,omitempty"`
	Servers           []*Server              `json:"servers,omitempty"`
	Paths             *Paths                 `json:"paths,omitempty"`
	Webhooks          *OrderedMap[*PathItem] `json:"webhooks,omitempty"`
	Components        *Components            `json:"components,omitempty"`
	Security          []SecurityRequirement  `json:"security,omitzero"`
	Tags              []*Tag                 `json:"tags,omitempty"`
	ExternalDocs      *ExternalDocs          `json:"externalDocs,omitempty"`
	Extensions        *Extensions            `json:"-"`
}

type Info struct {
	Title          string      `json:"title"`
	Summary        string      `json:"summary,omitempty"`
	Description    string      `json:"description,omitempty"`
	TermsOfService string      `json:"termsOfService,omitempty"`
	Contact        *Contact    `json:"contact,omitempty"`
	License        *License    `json:"license,omitempty"`
	Version        string      `json:"version"`
	Extensions     *Extensions `json:"-"`
}

type Contact struct {
	Name       string      `json:"name,omitempty"`
	URL        string      `json:"url,omitempty"`
	Email      string      `json:"email,omitempty"`
	Extensions *Extensions `json:"-"`
}

type License struct {
	Name       string      `json:"name"`
	Identifier string      `json:"identifier,omitempty"`
	URL        string      `json:"url,omitempty"`
	Extensions *Extensions `json:"-"`
}

type Server struct {
	URL         string                       `json:"url"`
	Description string                       `json:"description,omitempty"`
	Name        string                       `json:"name,omitempty"`
	Variables   *OrderedMap[*ServerVariable] `json:"variables,omitempty"`
	Extensions  *Extensions                  `json:"-"`
}

type ServerVariable struct {
	Enum        []string    `json:"enum,omitempty"`
	Default     string      `json:"default"`
	Description string      `json:"description,omitempty"`
	Extensions  *Extensions `json:"-"`
}

type Components struct {
	Schemas         *OrderedMap[*Schema]         `json:"schemas,omitempty"`
	Responses       *OrderedMap[*Response]       `json:"responses,omitempty"`
	Parameters      *OrderedMap[*Parameter]      `json:"parameters,omitempty"`
	Examples        *OrderedMap[*Example]        `json:"examples,omitempty"`
	RequestBodies   *OrderedMap[*RequestBody]    `json:"requestBodies,omitempty"`
	Headers         *OrderedMap[*Header]         `json:"headers,omitempty"`
	SecuritySchemes *OrderedMap[*SecurityScheme] `json:"securitySchemes,omitempty"`
	Links           *OrderedMap[*Link]           `json:"links,omitempty"`
	Callbacks       *OrderedMap[*Callback]       `json:"callbacks,omitempty"`
	PathItems       *OrderedMap[*PathItem]       `json:"pathItems,omitempty"`
	MediaTypes      *OrderedMap[*MediaType]      `json:"mediaTypes,omitempty"`
	Extensions      *Extensions                  `json:"-"`
}

// Paths maps path templates such as "/pets/{id}" to their operations.
type Paths struct {
	Items      OrderedMap[*PathItem]
	Extensions *Extensions
}

func (p *Paths) UnmarshalJSON(data []byte) error {
	return unmarshalExtensibleMap(data, &p.Items, &p.Extensions)
}

func (p Paths) MarshalJSON() ([]byte, error) {
	return marshalExtensible(p.Items, p.Extensions)
}

// PathItem describes the operations available on a single path.
type PathItem struct {
	Ref string `json:"$ref,omitempty"`
	// Resolved is the object Ref points to, after following any chain of
	// references. It is set by the loader.
	Resolved             *PathItem               `json:"-"`
	Summary              string                  `json:"summary,omitempty"`
	Description          string                  `json:"description,omitempty"`
	Get                  *Operation              `json:"get,omitempty"`
	Put                  *Operation              `json:"put,omitempty"`
	Post                 *Operation              `json:"post,omitempty"`
	Delete               *Operation              `json:"delete,omitempty"`
	Options              *Operation              `json:"options,omitempty"`
	Head                 *Operation              `json:"head,omitempty"`
	Patch                *Operation              `json:"patch,omitempty"`
	Trace                *Operation              `json:"trace,omitempty"`
	Query                *Operation              `json:"query,omitempty"`
	AdditionalOperations *OrderedMap[*Operation] `json:"additionalOperations,omitempty"`
	Servers              []*Server               `json:"servers,omitempty"`
	Parameters           []*Parameter            `json:"parameters,omitempty"`
	Extensions           *Extensions             `json:"-"`
}

type Operation struct {
	Tags         []string               `json:"tags,omitempty"`
	Summary      string                 `json:"summary,omitempty"`
	Description  string                 `json:"description,omitempty"`
	ExternalDocs *ExternalDocs          `json:"externalDocs,omitempty"`
	OperationID  string                 `json:"operationId,omitempty"`
	Parameters   []*Parameter           `json:"parameters,omitempty"`
	RequestBody  *RequestBody           `json:"requestBody,omitempty"`
	Responses    *Responses             `json:"responses,omitempty"`
	Callbacks    *OrderedMap[*Callback] `json:"callbacks,omitempty"`
	Deprecated   bool                   `json:"deprecated,omitempty"`
	// Security distinguishes absent (nil, inherit the document's) from an
	// explicit empty list, which removes security for this operation.
	Security   []SecurityRequirement `json:"security,omitzero"`
	Servers    []*Server             `json:"servers,omitempty"`
	Extensions *Extensions           `json:"-"`
}

// Parameter is a Parameter Object or, when Ref is set, a Reference Object.
type Parameter struct {
	Ref string `json:"$ref,omitempty"`
	// Resolved is the object Ref points to, after following any chain of
	// references. It is set by the loader.
	Resolved        *Parameter              `json:"-"`
	Name            string                  `json:"name,omitempty"`
	In              string                  `json:"in,omitempty"`
	Description     string                  `json:"description,omitempty"`
	Required        bool                    `json:"required,omitempty"`
	Deprecated      bool                    `json:"deprecated,omitempty"`
	AllowEmptyValue bool                    `json:"allowEmptyValue,omitempty"`
	Style           string                  `json:"style,omitempty"`
	Explode         *bool                   `json:"explode,omitempty"`
	AllowReserved   *bool                   `json:"allowReserved,omitempty"`
	Schema          *Schema                 `json:"schema,omitempty"`
	Example         json.RawMessage         `json:"example,omitempty"`
	Examples        *OrderedMap[*Example]   `json:"examples,omitempty"`
	Content         *OrderedMap[*MediaType] `json:"content,omitempty"`
	Extensions      *Extensions             `json:"-"`
}

// RequestBody is a Request Body Object or, when Ref is set, a Reference Object.
type RequestBody struct {
	Ref string `json:"$ref,omitempty"`
	// Resolved is the object Ref points to, after following any chain of
	// references. It is set by the loader.
	Resolved    *RequestBody            `json:"-"`
	Description string                  `json:"description,omitempty"`
	Content     *OrderedMap[*MediaType] `json:"content,omitempty"`
	Required    bool                    `json:"required,omitempty"`
	Extensions  *Extensions             `json:"-"`
}

// MediaType is a Media Type Object or, when Ref is set, a Reference Object.
// ItemSchema, PrefixEncoding and ItemEncoding describe sequential media types
// such as JSON Lines and server-sent events.
type MediaType struct {
	Ref string `json:"$ref,omitempty"`
	// Resolved is the object Ref points to, after following any chain of
	// references. It is set by the loader.
	Resolved       *MediaType             `json:"-"`
	Schema         *Schema                `json:"schema,omitempty"`
	ItemSchema     *Schema                `json:"itemSchema,omitempty"`
	Example        json.RawMessage        `json:"example,omitempty"`
	Examples       *OrderedMap[*Example]  `json:"examples,omitempty"`
	Encoding       *OrderedMap[*Encoding] `json:"encoding,omitempty"`
	PrefixEncoding []*Encoding            `json:"prefixEncoding,omitempty"`
	ItemEncoding   *Encoding              `json:"itemEncoding,omitempty"`
	Extensions     *Extensions            `json:"-"`
}

type Encoding struct {
	ContentType    string                 `json:"contentType,omitempty"`
	Headers        *OrderedMap[*Header]   `json:"headers,omitempty"`
	Style          string                 `json:"style,omitempty"`
	Explode        *bool                  `json:"explode,omitempty"`
	AllowReserved  *bool                  `json:"allowReserved,omitempty"`
	Encoding       *OrderedMap[*Encoding] `json:"encoding,omitempty"`
	PrefixEncoding []*Encoding            `json:"prefixEncoding,omitempty"`
	ItemEncoding   *Encoding              `json:"itemEncoding,omitempty"`
	Extensions     *Extensions            `json:"-"`
}

// Responses maps status codes ("200", "4XX", "default") to responses.
type Responses struct {
	Items      OrderedMap[*Response]
	Extensions *Extensions
}

func (r *Responses) UnmarshalJSON(data []byte) error {
	return unmarshalExtensibleMap(data, &r.Items, &r.Extensions)
}

func (r Responses) MarshalJSON() ([]byte, error) {
	return marshalExtensible(r.Items, r.Extensions)
}

// Response is a Response Object or, when Ref is set, a Reference Object.
type Response struct {
	Ref string `json:"$ref,omitempty"`
	// Resolved is the object Ref points to, after following any chain of
	// references. It is set by the loader.
	Resolved    *Response               `json:"-"`
	Summary     string                  `json:"summary,omitempty"`
	Description string                  `json:"description,omitempty"`
	Headers     *OrderedMap[*Header]    `json:"headers,omitempty"`
	Content     *OrderedMap[*MediaType] `json:"content,omitempty"`
	Links       *OrderedMap[*Link]      `json:"links,omitempty"`
	Extensions  *Extensions             `json:"-"`
}

// Callback maps runtime expressions to the path items the API calls back.
type Callback struct {
	Ref        string
	Resolved   *Callback
	Items      OrderedMap[*PathItem]
	Extensions *Extensions
}

func (c *Callback) UnmarshalJSON(data []byte) error {
	var ref struct {
		Ref string `json:"$ref"`
	}
	if err := json.Unmarshal(data, &ref); err != nil {
		return err
	}
	if ref.Ref != "" {
		*c = Callback{Ref: ref.Ref}
		return nil
	}
	return unmarshalExtensibleMap(data, &c.Items, &c.Extensions)
}

func (c Callback) MarshalJSON() ([]byte, error) {
	if c.Ref != "" {
		return json.Marshal(struct {
			Ref string `json:"$ref"`
		}{c.Ref})
	}
	return marshalExtensible(c.Items, c.Extensions)
}

// Example is an Example Object or, when Ref is set, a Reference Object.
type Example struct {
	Ref string `json:"$ref,omitempty"`
	// Resolved is the object Ref points to, after following any chain of
	// references. It is set by the loader.
	Resolved        *Example        `json:"-"`
	Summary         string          `json:"summary,omitempty"`
	Description     string          `json:"description,omitempty"`
	DataValue       json.RawMessage `json:"dataValue,omitempty"`
	SerializedValue string          `json:"serializedValue,omitempty"`
	Value           json.RawMessage `json:"value,omitempty"`
	ExternalValue   string          `json:"externalValue,omitempty"`
	Extensions      *Extensions     `json:"-"`
}

// Link is a Link Object or, when Ref is set, a Reference Object.
type Link struct {
	Ref string `json:"$ref,omitempty"`
	// Resolved is the object Ref points to, after following any chain of
	// references. It is set by the loader.
	Resolved     *Link                        `json:"-"`
	OperationRef string                       `json:"operationRef,omitempty"`
	OperationID  string                       `json:"operationId,omitempty"`
	Parameters   *OrderedMap[json.RawMessage] `json:"parameters,omitempty"`
	RequestBody  json.RawMessage              `json:"requestBody,omitempty"`
	Description  string                       `json:"description,omitempty"`
	Server       *Server                      `json:"server,omitempty"`
	Extensions   *Extensions                  `json:"-"`
	// Operation is the operation OperationRef points to, set by the
	// loader.
	Operation *Operation `json:"-"`
}

// Header is a Header Object or, when Ref is set, a Reference Object.
type Header struct {
	Ref string `json:"$ref,omitempty"`
	// Resolved is the object Ref points to, after following any chain of
	// references. It is set by the loader.
	Resolved    *Header                 `json:"-"`
	Description string                  `json:"description,omitempty"`
	Required    bool                    `json:"required,omitempty"`
	Deprecated  bool                    `json:"deprecated,omitempty"`
	Style       string                  `json:"style,omitempty"`
	Explode     *bool                   `json:"explode,omitempty"`
	Schema      *Schema                 `json:"schema,omitempty"`
	Example     json.RawMessage         `json:"example,omitempty"`
	Examples    *OrderedMap[*Example]   `json:"examples,omitempty"`
	Content     *OrderedMap[*MediaType] `json:"content,omitempty"`
	Extensions  *Extensions             `json:"-"`
}

type Tag struct {
	Name         string        `json:"name"`
	Summary      string        `json:"summary,omitempty"`
	Description  string        `json:"description,omitempty"`
	ExternalDocs *ExternalDocs `json:"externalDocs,omitempty"`
	Parent       string        `json:"parent,omitempty"`
	Kind         string        `json:"kind,omitempty"`
	Extensions   *Extensions   `json:"-"`
}

type ExternalDocs struct {
	Description string      `json:"description,omitempty"`
	URL         string      `json:"url"`
	Extensions  *Extensions `json:"-"`
}

// SecurityScheme is a Security Scheme Object or, when Ref is set, a
// Reference Object.
type SecurityScheme struct {
	Ref string `json:"$ref,omitempty"`
	// Resolved is the object Ref points to, after following any chain of
	// references. It is set by the loader.
	Resolved          *SecurityScheme `json:"-"`
	Type              string          `json:"type,omitempty"`
	Description       string          `json:"description,omitempty"`
	Name              string          `json:"name,omitempty"`
	In                string          `json:"in,omitempty"`
	Scheme            string          `json:"scheme,omitempty"`
	BearerFormat      string          `json:"bearerFormat,omitempty"`
	Flows             *OAuthFlows     `json:"flows,omitempty"`
	OpenIDConnectURL  string          `json:"openIdConnectUrl,omitempty"`
	OAuth2MetadataURL string          `json:"oauth2MetadataUrl,omitempty"`
	Deprecated        bool            `json:"deprecated,omitempty"`
	Extensions        *Extensions     `json:"-"`
}

type OAuthFlows struct {
	Implicit            *OAuthFlow  `json:"implicit,omitempty"`
	Password            *OAuthFlow  `json:"password,omitempty"`
	ClientCredentials   *OAuthFlow  `json:"clientCredentials,omitempty"`
	AuthorizationCode   *OAuthFlow  `json:"authorizationCode,omitempty"`
	DeviceAuthorization *OAuthFlow  `json:"deviceAuthorization,omitempty"`
	Extensions          *Extensions `json:"-"`
}

type OAuthFlow struct {
	AuthorizationURL       string              `json:"authorizationUrl,omitempty"`
	DeviceAuthorizationURL string              `json:"deviceAuthorizationUrl,omitempty"`
	TokenURL               string              `json:"tokenUrl,omitempty"`
	RefreshURL             string              `json:"refreshUrl,omitempty"`
	Scopes                 *OrderedMap[string] `json:"scopes"`
	Extensions             *Extensions         `json:"-"`
}

// SecurityRequirement maps security scheme names to the scopes or roles
// required. An empty requirement means anonymous access is allowed.
type SecurityRequirement = OrderedMap[[]string]

type Discriminator struct {
	PropertyName   string              `json:"propertyName"`
	Mapping        *OrderedMap[string] `json:"mapping,omitempty"`
	DefaultMapping string              `json:"defaultMapping,omitempty"`
	Extensions     *Extensions         `json:"-"`
	// Targets are the schemas the Mapping values name, by the same keys,
	// and DefaultTarget the one DefaultMapping names. A value is a
	// component schema name or a URI reference. Set by the loader.
	Targets       *OrderedMap[*Schema] `json:"-"`
	DefaultTarget *Schema              `json:"-"`
}

type XML struct {
	NodeType   string      `json:"nodeType,omitempty"`
	Name       string      `json:"name,omitempty"`
	Namespace  string      `json:"namespace,omitempty"`
	Prefix     string      `json:"prefix,omitempty"`
	Attribute  bool        `json:"attribute,omitempty"`
	Wrapped    bool        `json:"wrapped,omitempty"`
	Extensions *Extensions `json:"-"`
}

// pathItemMethods are the fixed-field operations of a Path Item, in
// specification order.
var pathItemMethods = []string{"get", "put", "post", "delete", "options", "head", "patch", "trace", "query"}

func (pi *PathItem) fixedOperation(method string) *Operation {
	switch method {
	case "get":
		return pi.Get
	case "put":
		return pi.Put
	case "post":
		return pi.Post
	case "delete":
		return pi.Delete
	case "options":
		return pi.Options
	case "head":
		return pi.Head
	case "patch":
		return pi.Patch
	case "trace":
		return pi.Trace
	case "query":
		return pi.Query
	}
	return nil
}

// Operations iterates over every operation of the path item: the fixed
// fields first, keyed by lower-case method name, then additionalOperations,
// keyed by the method name as written.
func (pi *PathItem) Operations() iter.Seq2[string, *Operation] {
	return func(yield func(string, *Operation) bool) {
		if pi == nil {
			return
		}
		for _, m := range pathItemMethods {
			if op := pi.fixedOperation(m); op != nil && !yield(m, op) {
				return
			}
		}
		for m, op := range pi.AdditionalOperations.All() {
			if op != nil && !yield(m, op) {
				return
			}
		}
	}
}
