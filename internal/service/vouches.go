package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/hi-naresh/unsolved/internal/store"
	"github.com/jackc/pgx/v5"
)

// ProfileDomain is one domain on a profile: the user's tier there, how many
// members vouched for them (a count only, never who), and whether the viewer
// may vouch (a domain contributor or expert there, not the user themself).
type ProfileDomain struct {
	Slug          string
	Name          string
	Tier          string
	Vouches       int
	CanVouch      bool
	ViewerVouched bool
}

type profileDomainJSON struct {
	ID            int16  `json:"id"`
	Slug          string `json:"slug"`
	Name          string `json:"name"`
	Tier          string `json:"tier"`
	Vouches       int    `json:"vouches"`
	ViewerTier    string `json:"viewer_tier"`
	ViewerVouched bool   `json:"viewer_vouched"`
}

// loadProfileReputation fills links, domains and the declared-history rule
// from one query.
func (s *Service) loadProfileReputation(ctx context.Context, p *Profile, userID uuid.UUID, viewer *uuid.UUID) error {
	self := viewer != nil && *viewer == userID
	var other *uuid.UUID
	if viewer != nil && !self {
		other = viewer
	}
	row, err := s.Store.GetProfileReputation(ctx, store.GetProfileReputationParams{
		UserID: userID, IncludeLinks: p.InDirectory, ViewerID: other,
	})
	if err != nil {
		return err
	}
	var links []ProfileLink
	var raw []struct {
		Provider string `json:"provider"`
		URL      string `json:"url"`
	}
	if err := json.Unmarshal(row.Links, &raw); err != nil {
		return fmt.Errorf("decode profile links: %w", err)
	}
	for _, l := range raw {
		if strings.HasPrefix(l.URL, "https://") {
			links = append(links, ProfileLink{Provider: l.Provider, URL: l.URL})
		}
	}
	var doms []profileDomainJSON
	if err := json.Unmarshal(row.Domains, &doms); err != nil {
		return fmt.Errorf("decode profile domains: %w", err)
	}
	p.Links = links
	p.ViewerIsSelf = self
	p.DeclaredCollapsed = row.ReachedContributor
	for _, d := range doms {
		p.Domains = append(p.Domains, ProfileDomain{
			Slug: d.Slug, Name: d.Name, Tier: d.Tier, Vouches: d.Vouches,
			CanVouch: other != nil && provenTier(d.ViewerTier), ViewerVouched: d.ViewerVouched,
		})
	}
	return nil
}

// VouchResult is the state after a vouch toggle.
type VouchResult struct {
	Vouched bool
	Handle  string // the vouchee's handle (for the redirect)
}

// ToggleVouch vouches for (or withdraws a vouch for) the user with handle in
// the domain with slug domainSlug. Only a domain_contributor or
// domain_expert in that domain may vouch; nobody vouches for themself.
// Withdrawing is always allowed. RecomputeStanding for the vouchee is
// enqueued in the same transaction.
func (s *Service) ToggleVouch(ctx context.Context, voucherID uuid.UUID, handle, domainSlug string) (VouchResult, error) {
	var res VouchResult
	u, err := s.Store.GetUserByHandle(ctx, NormalizeHandle(handle))
	if err != nil {
		return res, notFound(err)
	}
	if u.DeletedAt != nil {
		return res, ErrNotFound
	}
	res.Handle = u.Handle
	if u.ID == voucherID {
		return res, ErrForbidden
	}
	d, err := s.Store.GetDomainBySlug(ctx, strings.TrimSpace(domainSlug))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return res, Invalid("domain", "Choose a domain.")
		}
		return res, err
	}
	tier, err := s.Store.GetStandingTier(ctx, store.GetStandingTierParams{UserID: voucherID, DomainID: d.ID})
	if err != nil {
		return res, err
	}
	err = s.Store.InTx(ctx, func(q *store.Queries, tx pgx.Tx) error {
		key := store.DeleteVouchParams{VoucherID: voucherID, VoucheeID: u.ID, DomainID: d.ID}
		n, err := q.DeleteVouch(ctx, key)
		if err != nil {
			return err
		}
		if n == 0 {
			if !provenTier(tier) {
				return ErrForbidden
			}
			if _, err := q.InsertVouch(ctx, store.InsertVouchParams{
				VoucherID: voucherID, VoucheeID: u.ID, DomainID: d.ID, CreatedAt: s.Now(),
			}); err != nil {
				return err
			}
			res.Vouched = true
		}
		return s.enqueueStanding(ctx, tx, u.ID, d.ID)
	})
	return res, err
}
