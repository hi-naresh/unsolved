// Package views holds the templ components. Authorship is rendered only by
// Author, so the anonymity rules live in one place: templates never see
// author_id.
package views

// AuthorRef is everything a page may know about who wrote something. Build it
// with NewAuthorRef from a store row; never put an author id in it.
type AuthorRef struct {
	Anonymous bool
	Handle    string // empty when Anonymous
	Deleted   bool   // account deleted: shown as "deleted user"
	Tier      string // standing_tier in the content's domain ("member" when unknown)
}

// NewAuthorRef builds an AuthorRef. display is the row's author_display
// ("named"/"anonymous"); handle and deletedAt come from the joined users row;
// tier from user_domain_standing (empty → member). For anonymous rows the
// handle is dropped here, before it can reach a template.
func NewAuthorRef(display, handle string, deleted bool, tier string) AuthorRef {
	if tier == "" {
		tier = "member"
	}
	if display == "anonymous" {
		return AuthorRef{Anonymous: true, Tier: tier}
	}
	if deleted {
		return AuthorRef{Deleted: true, Tier: tier}
	}
	return AuthorRef{Handle: handle, Tier: tier}
}

// TierLabel is the human name of a standing tier.
func TierLabel(tier string) string {
	switch tier {
	case "declared_background":
		return "declared background"
	case "domain_contributor":
		return "domain contributor"
	case "domain_expert":
		return "domain expert"
	default:
		return "member"
	}
}
