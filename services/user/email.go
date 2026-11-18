// Copyright 2024 The Gitea Authors. All rights reserved.
// Copyright 2024 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package user

import (
	"context"
	"errors"
	"strings"

	auth_model "forgejo.org/models/auth"
	"forgejo.org/models/db"
	user_model "forgejo.org/models/user"
	"forgejo.org/modules/setting"
	"forgejo.org/modules/util"
	"forgejo.org/modules/validation"
	"forgejo.org/services/mailer"
)

// AdminAddOrSetPrimaryEmailAddress is used by admins to add or set a user's primary email address
func AdminAddOrSetPrimaryEmailAddress(ctx context.Context, u *user_model.User, emailStr string) error {
	if strings.EqualFold(u.Email, emailStr) {
		return nil
	}

	if err := validation.ValidateEmailForAdmin(emailStr); err != nil {
		return err
	}

	// Check if address exists already
	email, err := user_model.GetEmailAddressByEmail(ctx, emailStr)
	if err != nil && !errors.Is(err, util.ErrNotExist) {
		return err
	}
	if email != nil && email.UID != u.ID {
		return user_model.ErrEmailAlreadyUsed{Email: emailStr}
	}

	// Update old primary address
	primary, err := user_model.GetPrimaryEmailAddressOfUser(ctx, u.ID)
	if err != nil {
		return err
	}

	primary.IsPrimary = false
	if err := user_model.UpdateEmailAddress(ctx, primary); err != nil {
		return err
	}

	// Insert new or update existing address
	if email != nil {
		email.IsPrimary = true
		email.IsActivated = true
		if err := user_model.UpdateEmailAddress(ctx, email); err != nil {
			return err
		}
	} else {
		email = &user_model.EmailAddress{
			UID:         u.ID,
			Email:       emailStr,
			IsActivated: true,
			IsPrimary:   true,
		}
		if _, err := user_model.InsertEmailAddress(ctx, email); err != nil {
			return err
		}
	}

	u.Email = emailStr

	return user_model.UpdateUserCols(ctx, u, "email")
}

func ReplacePrimaryEmailAddress(ctx context.Context, u *user_model.User, emailStr string) error {
	if strings.EqualFold(u.Email, emailStr) {
		return nil
	}

	if err := validation.ValidateEmail(emailStr); err != nil {
		return err
	}

	if !u.IsOrganization() {
		// Check if address exists already
		email, err := user_model.GetEmailAddressByEmail(ctx, emailStr)
		if err != nil && !errors.Is(err, util.ErrNotExist) {
			return err
		}
		if email != nil {
			if email.IsPrimary && email.UID == u.ID {
				return nil
			}
			return user_model.ErrEmailAlreadyUsed{Email: emailStr}
		}

		// Remove old primary address
		primary, err := user_model.GetPrimaryEmailAddressOfUser(ctx, u.ID)
		if err != nil {
			return err
		}
		if _, err := db.DeleteByID[user_model.EmailAddress](ctx, primary.ID); err != nil {
			return err
		}

		// Insert new primary address
		email = &user_model.EmailAddress{
			UID:         u.ID,
			Email:       emailStr,
			IsActivated: true,
			IsPrimary:   true,
		}
		if _, err := user_model.InsertEmailAddress(ctx, email); err != nil {
			return err
		}
	}

	u.Email = emailStr

	return user_model.UpdateUserCols(ctx, u, "email")
}

func AddEmailAddresses(ctx context.Context, u *user_model.User, emails []string) error {
	for _, emailStr := range emails {
		if err := validation.ValidateEmail(emailStr); err != nil {
			return err
		}

		// Check if address exists already
		email, err := user_model.GetEmailAddressByEmail(ctx, emailStr)
		if err != nil && !errors.Is(err, util.ErrNotExist) {
			return err
		}
		if email != nil {
			return user_model.ErrEmailAlreadyUsed{Email: emailStr}
		}

		// Insert new address
		email = &user_model.EmailAddress{
			UID:         u.ID,
			Email:       emailStr,
			IsActivated: !setting.Service.RegisterEmailConfirm,
			IsPrimary:   false,
		}
		if _, err := user_model.InsertEmailAddress(ctx, email); err != nil {
			return err
		}
	}

	return nil
}

// ReplaceInactivePrimaryEmail replaces the primary email of a given user, even if the primary is not yet activated.
func ReplaceInactivePrimaryEmail(ctx context.Context, oldEmail string, email *user_model.EmailAddress) error {
	user := &user_model.User{}
	has, err := db.GetEngine(ctx).ID(email.UID).Get(user)
	if err != nil {
		return err
	} else if !has {
		return user_model.ErrUserNotExist{
			UID: email.UID,
		}
	}

	err = AddEmailAddresses(ctx, user, []string{email.Email})
	if err != nil {
		return err
	}

	err = makeEmailAddressPrimary(ctx, user, email, false)
	if err != nil {
		return err
	}

	// Delete previous activation token.
	if err := auth_model.DeleteAuthTokenByUser(ctx, user.ID); err != nil {
		return err
	}

	return DeleteEmailAddresses(ctx, user, []string{oldEmail})
}

func DeleteEmailAddresses(ctx context.Context, u *user_model.User, emails []string) error {
	return db.WithTx(ctx, func(ctx context.Context) error {
		for _, emailStr := range emails {
			// Check if address exists
			email, err := user_model.GetEmailAddressOfUser(ctx, emailStr, u.ID)
			if err != nil {
				if user_model.IsErrEmailAddressNotExist(err) {
					continue
				}
				return err
			}
			if email.IsPrimary {
				return user_model.ErrPrimaryEmailCannotDelete{Email: emailStr}
			}

			// Remove address
			if _, err := db.DeleteByID[user_model.EmailAddress](ctx, email.ID); err != nil {
				return err
			}
		}

		return nil
	})
}

func MakeEmailAddressPrimary(ctx context.Context, u *user_model.User, newPrimaryEmail *user_model.EmailAddress, notify bool) error {
	if !newPrimaryEmail.IsActivated {
		return validation.ErrEmailNotActivated
	}
	return makeEmailAddressPrimary(ctx, u, newPrimaryEmail, notify)
}

func makeEmailAddressPrimary(ctx context.Context, u *user_model.User, newPrimaryEmail *user_model.EmailAddress, notify bool) error {
	ctx, committer, err := db.TxContext(ctx)
	if err != nil {
		return err
	}
	defer committer.Close()
	sess := db.GetEngine(ctx)

	oldPrimaryEmail := u.Email

	// If the user was reported as abusive, a shadow copy should be created before first update (of certain columns).
	if err = user_model.IfNeededCreateShadowCopyForUser(ctx, u.ID, u, "email"); err != nil {
		return err
	}

	// 1. Update user table
	u.Email = newPrimaryEmail.Email
	if _, err = sess.ID(u.ID).Cols("email").Update(u); err != nil {
		return err
	}

	// 2. Update old primary email
	if _, err = sess.Where("uid=? AND is_primary=?", u.ID, true).Cols("is_primary").Update(&user_model.EmailAddress{
		IsPrimary: false,
	}); err != nil {
		return err
	}

	// 3. update new primary email
	newPrimaryEmail.IsPrimary = true
	if _, err = sess.ID(newPrimaryEmail.ID).Cols("is_primary").Update(newPrimaryEmail); err != nil {
		return err
	}

	if err := committer.Commit(); err != nil {
		return err
	}

	if notify {
		return mailer.SendPrimaryMailChange(u, oldPrimaryEmail)
	}
	return nil
}
