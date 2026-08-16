package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/dialect"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// GroupImageSizeAccount binds accounts to a group for a specific image size tier (1K/2K/4K).
// Rows are optional per tier; when absent the default account_groups pool is used.
type GroupImageSizeAccount struct {
	ent.Schema
}

func (GroupImageSizeAccount) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entsql.Annotation{Table: "group_image_size_accounts"},
	}
}

func (GroupImageSizeAccount) Fields() []ent.Field {
	return []ent.Field{
		field.Int64("group_id"),
		field.String("size_tier").
			MaxLen(8).
			Comment("Image billing tier: 1K, 2K, or 4K"),
		field.Int64("account_id"),
		field.Int("priority").
			Default(50).
			Comment("Lower values are scheduled first within the size tier pool"),
		field.Time("created_at").
			Immutable().
			Default(time.Now).
			SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
	}
}

func (GroupImageSizeAccount) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("account", Account.Type).
			Unique().
			Required().
			Field("account_id"),
		edge.To("group", Group.Type).
			Unique().
			Required().
			Field("group_id"),
	}
}

func (GroupImageSizeAccount) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("group_id", "size_tier", "account_id").
			Unique(),
		index.Fields("group_id", "size_tier", "priority", "account_id"),
		index.Fields("account_id"),
	}
}
