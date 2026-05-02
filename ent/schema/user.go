package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"github.com/google/uuid"
)

// User holds the schema definition for the User entity.
type User struct {
	ent.Schema
}

// Fields of the User.
func (User) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("id", uuid.UUID{}).
			Default(uuid.New).
			Immutable(),
		field.String("username").
			NotEmpty().
			Unique().
			Comment("Canonical login identifier: local users use USERNAME, external users use USER@REALM"),
		field.Enum("auth_source").
			Values("local", "ldap").
			Default("local"),
		field.String("auth_realm").
			Optional().
			Nillable().
			Comment("Realm label for external identities, e.g. AD domain or LDAP realm"),
		field.String("external_id").
			Optional().
			Nillable().
			Comment("Stable upstream directory identifier, e.g. AD objectGUID"),
		field.String("email").
			Optional().
			Nillable().
			Unique().
			Comment("Mirrored contact address for notifications and optional login lookup"),
		field.String("display_name").
			NotEmpty(),
		field.String("password_hash").
			Optional().
			Nillable().
			Comment("Required for local users; unused for LDAP-backed users"),
		field.Bool("can_manage_all_shares").
			Default(false),
		field.Bool("can_manage_users").
			Default(false),
		field.Bool("disabled").
			Default(false),
		field.Time("created_at").
			Default(time.Now).
			Immutable(),
		field.Time("updated_at").
			Default(time.Now).
			UpdateDefault(time.Now),
	}
}

// Edges of the User.
func (User) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("shares", Share.Type),
	}
}
