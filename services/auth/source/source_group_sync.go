// Copyright 2022 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package source

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"sync"

	"forgejo.org/models"
	"forgejo.org/models/organization"
	user_model "forgejo.org/models/user"
	"forgejo.org/modules/container"
	"forgejo.org/modules/log"
	org_service "forgejo.org/services/org"
)

type syncType int

const (
	syncAdd syncType = iota
	syncRemove
)

// SyncGroupsToTeams maps authentication source groups to organization and team memberships
func SyncGroupsToTeams(ctx context.Context,
	user *user_model.User,
	loginSourceID int64,
	sourceUserGroups container.Set[string],
	sourceGroupTeamMapping map[string]map[string][]string,
	sourceGroupTeamRemoval bool,
	dynGroupMaps *DynGroupMaps,
	dynGroupMapsRemoval bool,
) error {
	orgCache := make(map[string]*organization.Organization)
	teamCache := make(map[string]*organization.Team)

	return SyncGroupsToTeamsCached(ctx, user, loginSourceID,
		sourceUserGroups, sourceGroupTeamMapping, sourceGroupTeamRemoval,
		dynGroupMaps, dynGroupMapsRemoval,
		orgCache, teamCache)
}

// SyncGroupsToTeamsCached maps authentication source groups to organization and team memberships
func SyncGroupsToTeamsCached(
	ctx context.Context,
	user *user_model.User,
	loginSourceID int64,
	sourceUserGroups container.Set[string],
	sourceGroupTeamMapping map[string]map[string][]string,
	sourceGroupTeamRemoval bool,
	dynGroupMaps *DynGroupMaps,
	dynGroupMapsRemoval bool,
	orgCache map[string]*organization.Organization,
	teamCache map[string]*organization.Team,
) error {
	membershipsToAdd, membershipsToRemove := resolveMappedMemberships(
		ctx, user,
		sourceUserGroups, sourceGroupTeamMapping,
		dynGroupMaps, dynGroupMapsRemoval,
	)

	if sourceGroupTeamRemoval || dynGroupMapsRemoval {
		if err := syncGroupsToTeamsCached(ctx, user, loginSourceID, membershipsToRemove, syncRemove, orgCache, teamCache); err != nil {
			return fmt.Errorf("could not sync[remove] user groups: %w", err)
		}
	}

	if err := syncGroupsToTeamsCached(ctx, user, loginSourceID, membershipsToAdd, syncAdd, orgCache, teamCache); err != nil {
		return fmt.Errorf("could not sync[add] user groups: %w", err)
	}

	return nil
}

// DynGroupMaps are dynamic group to organization team mappings.
type DynGroupMaps struct {
	regexes []*regexp.Regexp
}

// Find checks whether group matches a dynamic group to organization team
// mapping and returns the name of the organization and of the team.
func (d *DynGroupMaps) Find(group string) (string, string) {
	if d == nil {
		return "", ""
	}

	group = strings.ToLower(group)
	for _, r := range d.regexes {
		// check if group matches regex
		match := r.FindStringSubmatch(group)
		if match == nil {
			continue
		}

		// match, try to get org and team
		org := ""
		team := ""
		for i, name := range r.SubexpNames() {
			switch name {
			case "org":
				org = match[i]
			case "team":
				team = match[i]
			}
		}
		return org, team
	}

	return "", ""
}

// Empty returns whether the dynamic group to organization team mappings
// are empty.
func (d *DynGroupMaps) Empty() bool {
	return d == nil || len(d.regexes) == 0
}

// NewDynGroupMaps returns new dynamic group to organzation team mappings.
func NewDynGroupMaps(list []string) *DynGroupMaps {
	d := &DynGroupMaps{
		regexes: []*regexp.Regexp{},
	}
	for _, s := range list {
		// replace placeholders with regex
		s = strings.ToLower(s)
		if strings.Count(s, "{org}") != 1 {
			log.Error("group sync: invalid number of {org} placeholders in %s", s)
			continue
		}
		if strings.Count(s, "{team}") != 1 {
			log.Error("group sync: invalid number of {team} placeholders in %s", s)
			continue
		}
		s = regexp.QuoteMeta(s)
		s = strings.Replace(s, "\\{org\\}", `(?<org>[\w-.]+)`, 1)
		s = strings.Replace(s, "\\{team\\}", `(?<team>[\w-.]+)`, 1)
		s = fmt.Sprintf("^%s$", s)

		// skip duplicates
		if slices.ContainsFunc(d.regexes, func(r *regexp.Regexp) bool {
			return r.String() == s
		}) {
			continue
		}

		// create regex
		r, err := regexp.Compile(s)
		if err != nil {
			log.Error("group sync: could not compile regex: %v", err)
			continue
		}
		d.regexes = append(d.regexes, r)
	}
	return d
}

// sourceDynGroupMaps contains the dynamic group to organization team mappings
// for the authentication sources.
var sourceDynGroupMaps struct {
	sync.Mutex
	d map[int64]*DynGroupMaps
}

// GetDynGroupMaps returns the dynamic group to organization team mappings of
// the authentication source identified by its source ID. If the mappings do
// not exist yet, they are created using the entries in list.
func GetDynGroupMaps(sourceID int64, list []string) *DynGroupMaps {
	sourceDynGroupMaps.Lock()
	defer sourceDynGroupMaps.Unlock()

	if sourceDynGroupMaps.d == nil {
		sourceDynGroupMaps.d = make(map[int64]*DynGroupMaps)
	}
	if sourceDynGroupMaps.d[sourceID] == nil {
		sourceDynGroupMaps.d[sourceID] = NewDynGroupMaps(list)
	}

	return sourceDynGroupMaps.d[sourceID]
}

// RemoveDynGroupMaps removes the dynamic group to organization team mappings
// of the authentication source identified by its source ID.
func RemoveDynGroupMaps(sourceID int64) {
	sourceDynGroupMaps.Lock()
	defer sourceDynGroupMaps.Unlock()

	if sourceDynGroupMaps.d == nil {
		return
	}
	sourceDynGroupMaps.d[sourceID] = nil
}

// getMembershipsToRemoveNotAdded returns memberships to remove.
// It returns all current memberships of the user that are not added based on
// the group team mappings in membershipsToAdd as memberships to remove.
func getMembershipsToRemoveNotAdded(
	ctx context.Context,
	user *user_model.User,
	membershipsToAdd map[string][]string,
) map[string][]string {
	membershipsToRemove := map[string][]string{}

	// get user's organizations
	orgs, err := organization.GetUserOrgsList(ctx, user)
	if err != nil {
		log.Warn("group sync: could not get organizations: %v", err)
		return membershipsToRemove
	}

	// get user's teams
	teams, err := organization.GetUserTeams(ctx, user.ID)
	if err != nil {
		log.Warn("group sync: could not get teams: %v", err)
		return membershipsToRemove
	}

	// check memberships
	for _, org := range orgs {
		for _, team := range teams {
			if team.OrgID != org.ID {
				continue
			}
			// keep membership if it's added via group team mapping
			if slices.Contains(membershipsToAdd[org.LowerName], team.LowerName) {
				continue
			}
			if team.IsOwnerTeam() && team.NumMembers == 1 {
				// skip team if user is last member of owner
				// team to avoid errors when actually removing
				// team memberships later
				log.Warn("group sync: skipping removal of user %s from owner team in org %s: user is last member",
					user.Name, org.Name)
				continue
			}
			membershipsToRemove[org.LowerName] = append(membershipsToRemove[org.LowerName], team.LowerName)
		}
	}

	return membershipsToRemove
}

func resolveMappedMemberships(
	ctx context.Context,
	user *user_model.User,
	sourceUserGroups container.Set[string],
	sourceGroupTeamMapping map[string]map[string][]string,
	dynGroupMaps *DynGroupMaps,
	dynGroupMapsRemoval bool,
) (map[string][]string, map[string][]string) {
	membershipsToAdd := map[string][]string{}
	membershipsToRemove := map[string][]string{}

	// static mappings
	for group, memberships := range sourceGroupTeamMapping {
		isUserInGroup := sourceUserGroups.Contains(group)
		for org, teams := range memberships {
			org = strings.ToLower(org)
			for i, team := range teams {
				teams[i] = strings.ToLower(team)
			}
			if isUserInGroup {
				membershipsToAdd[org] = append(membershipsToAdd[org], teams...)
			} else {
				membershipsToRemove[org] = append(membershipsToRemove[org], teams...)
			}
		}
	}

	// dynamic mappings
	if !dynGroupMaps.Empty() {
		for group := range sourceUserGroups {
			org, team := dynGroupMaps.Find(group)
			if org == "" || team == "" {
				// no matching mapping found or invalid mapping
				continue
			}
			if !slices.Contains(membershipsToAdd[org], team) {
				membershipsToAdd[org] = append(membershipsToAdd[org], team)
			}
		}
	}

	// dynamic removal
	if dynGroupMapsRemoval {
		membershipsToRemove = getMembershipsToRemoveNotAdded(ctx, user, membershipsToAdd)
	}

	return membershipsToAdd, membershipsToRemove
}

func syncGroupsToTeamsCached(ctx context.Context, user *user_model.User, loginSourceID int64, orgTeamMap map[string][]string, action syncType, orgCache map[string]*organization.Organization, teamCache map[string]*organization.Team) error {
	for orgName, teamNames := range orgTeamMap {
		var err error
		org, ok := orgCache[orgName]
		if !ok {
			org, err = organization.GetOrgByName(ctx, orgName)
			if err != nil {
				if organization.IsErrOrgNotExist(err) {
					// organization must be created before group sync
					log.Warn("group sync: Could not find organisation %s: %v", orgName, err)
					continue
				}
				return err
			}
			orgCache[orgName] = org
		}
		for _, teamName := range teamNames {
			team, ok := teamCache[orgName+teamName]
			if !ok {
				team, err = org.GetTeam(ctx, teamName)
				if err != nil {
					if organization.IsErrTeamNotExist(err) {
						// team must be created before group sync
						log.Warn("group sync: Could not find team %s: %v", teamName, err)
						continue
					}
					return err
				}
				teamCache[orgName+teamName] = team
			}

			isMember, err := organization.IsTeamMember(ctx, org.ID, team.ID, user.ID)
			if err != nil {
				return err
			}

			if action == syncAdd && !isMember {
				if err := org_service.AddTeamMemberByLoginSource(ctx, team, user.ID, loginSourceID); err != nil {
					log.Error("group sync: Could not add user to team: %v", err)
					return err
				}
			} else if action == syncRemove && isMember {
				if err := models.RemoveTeamMember(ctx, team, user.ID); err != nil {
					log.Error("group sync: Could not remove user from team: %v", err)
					return err
				}
			}
		}
	}
	return nil
}
