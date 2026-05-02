package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"github.com/google/uuid"
)

// Share holds the schema definition for the Share entity.
type Share struct {
	ent.Schema
}

// Fields of the Share.
func (Share) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("id", uuid.UUID{}).
			Default(uuid.New),
		field.UUID("owner_id", uuid.UUID{}).
			Optional().
			Nillable().
			Comment("Local owner user responsible for the share"),
		field.Enum("type").
			Values("download", "upload").
			Default("download"),
		field.String("public_token_encrypted").
			Optional().
			Nillable(),
		field.String("token_hash").
			Unique(),
		field.String("password_hash").
			Optional().
			Nillable(),
		field.Int("unlock_version").
			Default(1).
			Comment("Server-validated unlock session version; increment when rotating the share password"),
		field.String("title").
			NotEmpty().
			Default("Legacy Share").
			Comment("A mandatory short title for usability"),
		field.String("note").
			Optional().
			Nillable().
			Comment("An optional note/message for guest instructions"),
		field.Time("expires_at"),
		field.Time("created_at").
			Default(time.Now),
	}
}

// Edges of the Share.
func (Share) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("owner", User.Type).
			Ref("shares").
			Field("owner_id").
			Unique(),
		edge.To("files", File.Type),
	}
}
