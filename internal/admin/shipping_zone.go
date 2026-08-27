package admin

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/i18n"
)

// MaxZonePrefixes bounds one submission of postal prefixes.
const MaxZonePrefixes = 100

// NewZone is a delivery zone being created, with the postal prefixes that reach it.
type NewZone struct {
	Code     string
	Name     string
	NameEn   string
	Prefixes string
}

// CreateZone adds a zone and its prefixes together: an empty zone is unreachable.
func (s *Store) CreateZone(ctx context.Context, z *NewZone) (map[string]string, error) {
	z.Code = strings.ToLower(strings.TrimSpace(z.Code))
	z.Name = strings.TrimSpace(z.Name)
	z.NameEn = strings.TrimSpace(z.NameEn)

	errs := map[string]string{}
	if !methodCodeFormat.MatchString(z.Code) {
		errs["zone_code"] = i18n.T(ctx, i18n.KeyFormZoneCode)
	}
	if z.Name == "" || utf8.RuneCountInString(z.Name) > MaxTaxonomyNameRunes {
		errs["zone_name"] = i18n.T(ctx, i18n.KeyFormNameRequired)
	}
	prefixes, prefixErr := parsePrefixes(ctx, z.Prefixes)
	if prefixErr != "" {
		errs["prefixes"] = prefixErr
	}
	if len(errs) > 0 {
		return errs, nil
	}

	if err := s.audited(ctx, Event{
		Action: ActionCreateShippingZone, Table: "shipping_zones",
		After: map[string]any{
			"code": z.Code, "name": z.Name, "prefixes": len(prefixes),
		},
	}, func(ctx context.Context, q *db.Queries) error {
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
		if takenBy(err, "shipping_zones_code_key") {
			return map[string]string{"zone_code": i18n.T(ctx, i18n.KeyFormZoneCodeTaken)}, nil
		}
		return nil, fmt.Errorf("%w: %w", ErrRefused, err)
	}
	return nil, nil
}

// SetZonePrefixes upserts each prefix; delete-then-insert leaves a gap.
func (s *Store) SetZonePrefixes(ctx context.Context, id, list string) (map[string]string, error) {
	zoneID, err := uuid.Parse(id)
	if err != nil {
		return nil, ErrNotFound
	}
	prefixes, prefixErr := parsePrefixes(ctx, list)
	if prefixErr != "" {
		return map[string]string{"prefixes": prefixErr}, nil
	}

	if err := s.audited(ctx, Event{
		Action: ActionSetZonePrefixes, Table: "shipping_zone_prefixes",
		ID:    nullableID(zoneID),
		After: map[string]any{"prefixes": len(prefixes)},
	}, func(ctx context.Context, q *db.Queries) error {
		for _, prefix := range prefixes {
			if assignErr := q.AssignZonePrefix(ctx, db.AssignZonePrefixParams{
				Prefix: prefix, ZoneID: zoneID,
			}); assignErr != nil {
				return assignErr
			}
		}
		return nil
	}); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrRefused, err)
	}
	return nil, nil
}

// RemoveZonePrefix takes one prefix out of one zone.
func (s *Store) RemoveZonePrefix(ctx context.Context, id, prefix string) error {
	zoneID, err := uuid.Parse(id)
	if err != nil {
		return ErrNotFound
	}
	prefix = strings.TrimSpace(prefix)
	return s.audited(ctx, Event{
		Action: ActionSetZonePrefixes, Table: "shipping_zone_prefixes",
		ID:     nullableID(zoneID),
		Before: map[string]any{"prefix": prefix},
	}, func(ctx context.Context, q *db.Queries) error {
		n, execErr := q.RemoveZonePrefix(ctx, db.RemoveZonePrefixParams{
			Prefix: prefix, ZoneID: zoneID,
		})
		if execErr != nil {
			return fmt.Errorf("%w: %w", ErrRefused, execErr)
		}
		if n == 0 {
			return ErrNotFound
		}
		return nil
	})
}

// DeleteZone removes a zone nothing points at.
func (s *Store) DeleteZone(ctx context.Context, id string) error {
	zoneID, err := uuid.Parse(id)
	if err != nil {
		return ErrNotFound
	}
	return s.audited(ctx, Event{
		Action: ActionDeleteShippingZone, Table: "shipping_zones",
		ID: nullableID(zoneID),
	}, func(ctx context.Context, q *db.Queries) error {
		n, execErr := q.DeleteShippingZone(ctx, zoneID)
		if execErr != nil {
			return fmt.Errorf("%w: %w", ErrRefused, execErr)
		}
		if n == 0 {
			return ErrInUse
		}
		return nil
	})
}

func parsePrefixes(ctx context.Context, list string) (prefixes []string, message string) {
	fields := strings.FieldsFunc(list, func(r rune) bool {
		return r == ',' || r == ';' || r == '\n' || r == '\r' || r == '\t' || r == ' '
	})
	if len(fields) == 0 {
		return nil, i18n.T(ctx, i18n.KeyFormZonePrefixRequired)
	}
	if len(fields) > MaxZonePrefixes {
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

var zonePrefixFormat = regexp.MustCompile(`^\d{3}$`)
