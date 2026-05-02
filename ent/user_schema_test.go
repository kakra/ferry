package ent_test

import (
	"context"
	"testing"
	"time"

	"github.com/kakra/ferry/ent/enttest"
	"github.com/kakra/ferry/ent/share"
	"github.com/kakra/ferry/ent/user"
	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUserSchema_LocalUserAndShareOwnership(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:ent?mode=memory&cache=shared&_fk=1")
	defer client.Close()

	ctx := context.Background()

	u, err := client.User.Create().
		SetUsername("admin").
		SetAuthSource(user.AuthSourceLocal).
		SetDisplayName("Admin User").
		SetPasswordHash("hashed-password").
		SetCanManageAllShares(true).
		SetCanManageUsers(true).
		Save(ctx)
	require.NoError(t, err)

	assert.Equal(t, "admin", u.Username)
	assert.Equal(t, user.AuthSourceLocal, u.AuthSource)
	assert.Nil(t, u.AuthRealm)
	assert.Nil(t, u.Email)
	assert.Equal(t, "hashed-password", *u.PasswordHash)

	ownerlessShare, err := client.Share.Create().
		SetTitle("Ownerless Share").
		SetTokenHash("ownerless-token").
		SetExpiresAt(time.Now().Add(time.Hour)).
		Save(ctx)
	require.NoError(t, err)
	assert.Nil(t, ownerlessShare.OwnerID)

	ownedShare, err := client.Share.Create().
		SetTitle("Owned Share").
		SetTokenHash("owned-token").
		SetExpiresAt(time.Now().Add(time.Hour)).
		SetOwner(u).
		Save(ctx)
	require.NoError(t, err)
	require.NotNil(t, ownedShare.OwnerID)
	assert.Equal(t, u.ID, *ownedShare.OwnerID)

	shareOwner, err := ownedShare.QueryOwner().Only(ctx)
	require.NoError(t, err)
	assert.Equal(t, u.ID, shareOwner.ID)

	shares, err := client.User.Query().
		Where(user.IDEQ(u.ID)).
		QueryShares().
		Order(share.ByTitle()).
		All(ctx)
	require.NoError(t, err)
	require.Len(t, shares, 1)
	assert.Equal(t, ownedShare.ID, shares[0].ID)
}

func TestUserSchema_UserUniqueness(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:ent?mode=memory&cache=shared&_fk=1")
	defer client.Close()

	ctx := context.Background()

	_, err := client.User.Create().
		SetUsername("alice").
		SetAuthSource(user.AuthSourceLocal).
		SetDisplayName("Alice One").
		SetPasswordHash("hash-1").
		SetEmail("alice@example.test").
		Save(ctx)
	require.NoError(t, err)

	_, err = client.User.Create().
		SetUsername("alice").
		SetAuthSource(user.AuthSourceLocal).
		SetDisplayName("Alice Two").
		SetPasswordHash("hash-2").
		Save(ctx)
	require.Error(t, err)

	_, err = client.User.Create().
		SetUsername("alice@example.realm").
		SetAuthSource(user.AuthSourceLdap).
		SetAuthRealm("EXAMPLE.REALM").
		SetDisplayName("Alice LDAP").
		SetEmail("alice@example.test").
		Save(ctx)
	require.Error(t, err)
}
