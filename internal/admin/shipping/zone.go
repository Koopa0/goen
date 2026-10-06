package shipping

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/koopa0/goen/internal/admin/audit"
	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/pgerr"
	"github.com/koopa0/goen/internal/postcode"
)

const maxZonePrefixes = 100

type NewZone struct {
	Code     string
	Name     string
	NameEn   string
	Prefixes string
}

func (s *Store) CreateZone(ctx context.Context, z *NewZone) (map[string]string, error) {
	z.Code = strings.ToLower(strings.TrimSpace(z.Code))
	z.Name = strings.TrimSpace(z.Name)
	z.NameEn = strings.TrimSpace(z.NameEn)

	errs := map[string]string{}
	if !methodCodeFormat.MatchString(z.Code) {
		errs["zone_code"] = i18n.T(ctx, i18n.KeyFormZoneCode)
	}
	if z.Name == "" || utf8.RuneCountInString(z.Name) > MaxNameRunes {
		errs["zone_name"] = i18n.T(ctx, i18n.KeyFormNameRequired)
	}
	prefixes, prefixErr := parseRequiredPrefixes(ctx, z.Prefixes)
	if prefixErr != "" {
		errs["prefixes"] = prefixErr
	}
	if len(errs) > 0 {
		return errs, nil
	}

	if err := audit.Run(ctx, s.pool, audit.Event{
		Action: audit.ActionCreateShippingZone, Table: "shipping_zones",
		After: map[string]any{
			"code": z.Code, "name": z.Name, "prefixes": len(prefixes),
		},
	}, func(ctx context.Context, q *db.Queries) error {
		if lockErr := q.LockZonePrefixAssignments(ctx); lockErr != nil {
			return fmt.Errorf("lock zone prefix assignments: %w", lockErr)
		}
		zoneID, insErr := q.CreateShippingZone(ctx, db.CreateShippingZoneParams{
			Code: z.Code, Name: z.Name, NameEn: z.NameEn,
		})
		if insErr != nil {
			return insErr
		}
		for _, prefix := range prefixes {
			if assignErr := q.AssignZonePrefix(ctx, db.AssignZonePrefixParams{
				Prefix: prefix, ZoneID: zoneID,
			}); assignErr != nil {
				return assignErr
			}
		}
		return nil
	}); err != nil {
		if pgerr.IsConstraint(err, "shipping_zones_code_key") {
			return map[string]string{"zone_code": i18n.T(ctx, i18n.KeyFormZoneCodeTaken)}, nil
		}
		return nil, pgerr.WrapRefusal(err, ErrRefused)
	}
	return nil, nil
}

// SetZonePrefixes replaces the zone's whole set with the submitted one. The
// prefix assignments, then the zone row, are locked and assignments run before the
// zone-scoped sweep, all in one transaction, so concurrent forms can neither
// deadlock nor commit a union, and moving a prefix never exposes a moment when
// it belongs to no zone.
func (s *Store) SetZonePrefixes(ctx context.Context, id, list string) (map[string]string, error) {
	zoneID, err := uuid.Parse(id)
	if err != nil {
		return nil, ErrNotFound
	}
	prefixes, prefixErr := parsePrefixes(ctx, list)
	if prefixErr != "" {
		return map[string]string{"zone_prefixes": prefixErr}, nil
	}

	if err := audit.Run(ctx, s.pool, audit.Event{
		Action: audit.ActionSetZonePrefixes, Table: "shipping_zone_prefixes",
		ID:    audit.EntityID(zoneID),
		After: map[string]any{"prefixes": len(prefixes)},
	}, func(ctx context.Context, q *db.Queries) error {
		if lockErr := q.LockZonePrefixAssignments(ctx); lockErr != nil {
			return fmt.Errorf("lock zone prefix assignments: %w", lockErr)
		}
		if _, lockErr := q.LockShippingZone(ctx, zoneID); lockErr != nil {
			if errors.Is(lockErr, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return fmt.Errorf("lock shipping zone: %w", lockErr)
		}
		for _, prefix := range prefixes {
			if assignErr := q.AssignZonePrefix(ctx, db.AssignZonePrefixParams{
				Prefix: prefix, ZoneID: zoneID,
			}); assignErr != nil {
				return fmt.Errorf("assign zone prefix %q: %w", prefix, assignErr)
			}
		}
		if _, delErr := q.RemoveZonePrefixesExcept(ctx, db.RemoveZonePrefixesExceptParams{
			ZoneID: zoneID, Keep: prefixes,
		}); delErr != nil {
			return fmt.Errorf("remove omitted zone prefixes: %w", delErr)
		}
		return nil
	}); err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("set zone prefixes: %w", err)
	}
	return nil, nil
}

func (s *Store) DeleteZone(ctx context.Context, id string) error {
	zoneID, err := uuid.Parse(id)
	if err != nil {
		return ErrNotFound
	}
	return audit.Run(ctx, s.pool, audit.Event{
		Action: audit.ActionDeleteShippingZone, Table: "shipping_zones",
		ID: audit.EntityID(zoneID),
	}, func(ctx context.Context, q *db.Queries) error {
		if lockErr := q.LockZonePrefixAssignments(ctx); lockErr != nil {
			return fmt.Errorf("lock zone prefix assignments: %w", lockErr)
		}
		if _, lockErr := q.LockShippingZone(ctx, zoneID); lockErr != nil {
			if errors.Is(lockErr, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return fmt.Errorf("lock shipping zone: %w", lockErr)
		}
		n, execErr := q.DeleteShippingZone(ctx, zoneID)
		if execErr != nil {
			return pgerr.WrapRefusal(execErr, ErrRefused)
		}
		if n != 0 {
			return nil
		}
		zones, readErr := q.AdminShippingZones(ctx)
		if readErr != nil {
			return fmt.Errorf("read shipping zone deletion blocker: %w", readErr)
		}
		for i := range zones {
			if zones[i].ID != zoneID {
				continue
			}
			// The prefix lock keeps this count stable. With no prefixes, the
			// DELETE's only other blocker is a delivery-version reference.
			use := ZoneUsedByVersions
			if zones[i].PrefixCount > 0 {
				use = ZoneUsedByPrefixes
			}
			return &ZoneInUseError{Use: use}
		}
		return ErrNotFound
	})
}

func parsePrefixes(ctx context.Context, list string) (prefixes []string, message string) {
	fields := postcode.Fields(list)
	if len(fields) == 0 {
		return []string{}, ""
	}
	if len(fields) > maxZonePrefixes {
		return nil, i18n.T(ctx, i18n.KeyFormZonePrefixTooMany)
	}
	seen := make(map[string]bool, len(fields))
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		if !zonePrefixFormat.MatchString(f) {
			return nil, fmt.Sprintf(i18n.T(ctx, i18n.KeyFormZonePrefixShape), f)
		}
		if seen[f] {
			continue
		}
		seen[f] = true
		out = append(out, f)
	}
	return out, ""
}

func parseRequiredPrefixes(ctx context.Context, list string) (prefixes []string, message string) {
	prefixes, message = parsePrefixes(ctx, list)
	if message == "" && len(prefixes) == 0 {
		return nil, i18n.T(ctx, i18n.KeyFormZonePrefixRequired)
	}
	return prefixes, message
}

var zonePrefixFormat = regexp.MustCompile(`^\d{3}$`)
