package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"github.com/google/uuid"
)

// File holds the schema definition for the File entity.
type File struct {
	ent.Schema
}

// Fields of the File.
func (File) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("id", uuid.UUID{}).
			Default(uuid.New).
			Immutable(),
		field.String("original_name"),
		field.UUID("upload_session_id", uuid.UUID{}).
			Optional().
			Nillable().
			Comment("Used to allow guests to delete their own uploads in the same session"),
		field.Enum("status").
			Values("active", "missing", "deleted").
			Default("active"),
		field.Time("created_at").
			Default(time.Now).
			Immutable(),
	}
}

// Edges of the File.
func (File) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("blob", Blob.Type).
			Ref("files").
			Unique().
			Required(),
		edge.From("share", Share.Type).
			Ref("files").
			Unique().
			Required(),
	}
}
