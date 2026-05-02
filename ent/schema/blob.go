package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
)

// Blob holds the schema definition for the Blob entity.
type Blob struct {
	ent.Schema
}

// Fields of the Blob.
func (Blob) Fields() []ent.Field {
	return []ent.Field{
		field.String("id").
			Unique().
			Immutable().
			Comment("SHA-256 hash of the file content"),
		field.Int64("size"),
		field.String("storage_path"),
		field.Time("unreachable_since").
			Optional().
			Nillable(),
		field.Time("created_at").
			Default(time.Now).
			Immutable(),
		field.Time("last_seen_at").
			Default(time.Now),
	}
}

// Edges of the Blob.
func (Blob) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("files", File.Type),
	}
}
