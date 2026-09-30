package service

import (
	"context"
	"github.com/hi-naresh/unsolved/internal/jobs"

	"github.com/google/uuid"
	"github.com/hi-naresh/unsolved/internal/store"
	"github.com/jackc/pgx/v5"
)

// SeedHandle is the system author of the launch seed problems.
const (
	SeedHandle      = "unsolved"
	SeedDisplayName = "Unsolved"
)

// SeedProblem is one launch seed problem (a first revision's text).
type SeedProblem struct {
	Domain         string // domains.slug
	Title          string
	CurrentProcess string
	Pain           string
	Tried          string
}

// SeedProblems are the five launch problems loaded by cmd/seed. Each is an
// operational process described as it runs today, not a build request.
var SeedProblems = []SeedProblem{
	{
		Domain: "manufacturing",
		Title:  "Shift handover on a machining line runs on a paper logbook nobody reads in full",
		CurrentProcess: `1. At 05:45 and 17:45 the outgoing shift lead writes the handover in an A4 logbook kept at the cell office: machine status, parts in progress, scrap, anything "to watch".
2. Operators add their own notes on sticky notes on the CNC doors (e.g. "tool 7 chattering, check offsets").
3. The incoming lead reads the logbook, walks the line, and asks whoever is still there.
4. Quality issues found overnight are also emailed to the day quality engineer, who re-types the important ones into the NCR spreadsheet.
5. Once a week the production manager flicks back through the logbook to find repeat problems for the Monday meeting.`,
		Pain:  `The walk-round overlaps the shift change by 15 minutes, so half the outgoing operators have left before questions are asked. Sticky notes fall off. Last quarter we ran 340 parts with a worn tool that the night shift had noticed but only written on a note. Finding repeat faults means reading a week of handwriting.`,
		Tried: `A shared Excel file on the office PC (nobody walked back to the office to fill it in). A whiteboard per cell (wiped by cleaners). We have not tried anything on the shop floor tablets because they are locked to the MES.`,
	},
	{
		Domain: "healthcare",
		Title:  "Dental practice recall reminders are done by hand from a printed list every Monday",
		CurrentProcess: `1. Every Monday the practice manager runs the "patients due for recall" report in our practice management system and prints it (usually 120-180 names across 3 dentists and 2 hygienists).
2. A receptionist works down the list: patients with a mobile get a text typed on the practice phone, the rest get a phone call between patients.
3. She ticks the list and writes "LM" (left message), "booked", or "call back" in pen.
4. Anyone not reached is carried onto next Monday's printout by hand.
5. Booked appointments are entered into the diary as usual; the printout is filed in a ring binder for the CQC inspection.`,
		Pain:  `It takes about 6 hours of reception time a week and gets dropped whenever the front desk is busy, which is always in January and September. Patients fall off the list when the carry-over is missed, and we find out months later when they turn up with a problem. The binder is the only record of who we contacted.`,
		Tried: `The PMS has a built-in SMS module but it charges per message and sends the same text to everyone, including patients who have already booked. We tried it for a month and got complaints. A temp for the backlog helped once but didn't fix the weekly routine.`,
	},
	{
		Domain: "logistics",
		Title:  "Small 3PL reconciles carrier invoices against shipments line by line in spreadsheets",
		CurrentProcess: `1. Each week four carriers send invoices as PDF plus a CSV (each carrier uses its own columns and references).
2. Our accounts assistant exports the week's despatches from the WMS to Excel.
3. She matches each invoice line to a despatch by consignment number, or by postcode + date when the carrier uses its own reference.
4. She checks weight, service level and surcharges (fuel, remote area, oversize) against the rate card PDF for that carrier.
5. Mismatches go into a "disputes" tab and she emails the carrier account manager with screenshots.
6. Agreed charges are re-billed to our clients per their contract margin, which is a second spreadsheet.`,
		Pain:  `Around 9,000 lines a week, two full days of work. Surcharges are where the money goes: we estimate we overpay 2-3% because checks get skipped at month end. Disputes older than 30 days are rejected by two carriers, and we often miss that window. Re-billing clients is late because it waits for reconciliation.`,
		Tried: `VLOOKUP templates per carrier (break whenever the carrier changes its CSV). A freight audit company quoted a percentage of recovered money that was more than our margin. Asked our WMS vendor; they said it's out of scope.`,
	},
	{
		Domain: "nonprofit",
		Title:  "Food bank volunteer rota is organised through three WhatsApp groups and a paper diary",
		CurrentProcess: `1. The volunteer coordinator (part-time, 2 days a week) keeps the rota in a paper diary: warehouse sorting Mon/Wed, distribution Tue/Thu/Sat, drivers daily.
2. Each Sunday she posts the week's gaps into three WhatsApp groups (warehouse, distribution, drivers).
3. Volunteers reply "I can do Thursday" in the group; she writes names into the diary.
4. Drop-outs arrive as private WhatsApp messages or texts, often the evening before, and she re-posts the gap.
5. Session leads phone her on the day to ask who is coming, and she reads the diary out.
6. For funder reports she counts volunteer hours from the diary at the end of each quarter.`,
		Pain:  `Replies get lost in busy groups, so we get two people on one shift and nobody on another. On her non-working days nobody else can see the diary. Twice this year a Saturday distribution opened with 3 of the 8 volunteers needed. The quarterly hours count takes a full day and our funder has questioned the numbers.`,
		Tried: `A free online sign-up sheet (older volunteers didn't use it and it didn't handle drop-outs). A shared Google Sheet (edits clashed and people deleted rows). WhatsApp polls help a bit for single shifts.`,
	},
	{
		Domain: "construction",
		Title:  "Snagging lists for new-build handovers are spread across photos, WhatsApp and a spreadsheet",
		CurrentProcess: `1. Before each plot handover the site manager walks the house with a clipboard and phone, photographing defects (paint, sealant, doors, missing fittings).
2. Back in the cabin he types each snag into an Excel sheet per plot: room, description, trade, and "see photo 14".
3. Photos are sent to each trade's WhatsApp by hand, grouped by trade.
4. Trades reply "done" on WhatsApp; he walks the plot again to check and marks the sheet.
5. The buyer's own snag list arrives by email two weeks after completion and is merged into the same sheet.
6. The sheet is emailed to the customer care team at head office for the NHBC warranty period.`,
		Pain:  `Photos and rows drift apart: "photo 14" on a phone that was replaced means nothing. Trades claim they were never told, and we have no record of when they were. A 60-plot phase generates 2,000+ snags and the site manager spends evenings typing. Handovers slip, and every slipped completion costs us penalty days with the buyer's mortgage lender.`,
		Tried: `A snagging app our group trialled needed every trade to have a paid login; most refused. A shared OneDrive folder per plot (photos uploaded, but nobody linked them to rows).`,
	},
}

// Seed loads the launch seed problems. It creates the system author
// (handle "unsolved") if needed, then inserts each seed problem with its first
// revision in one transaction. It is idempotent: a seed whose title already
// exists for the system author is skipped. It returns how many were created.
func (s *Service) Seed(ctx context.Context) (int, error) {
	uid, err := uuid.NewV7()
	if err != nil {
		return 0, err
	}
	authorID, err := s.Store.SeedUpsertSystemUser(ctx, store.SeedUpsertSystemUserParams{
		ID: uid, Handle: SeedHandle, DisplayName: SeedDisplayName,
	})
	if err != nil {
		return 0, err
	}
	created := 0
	for _, sp := range SeedProblems {
		exists, err := s.Store.SeedProblemExists(ctx, store.SeedProblemExistsParams{AuthorID: authorID, Title: sp.Title})
		if err != nil {
			return created, err
		}
		if exists {
			continue
		}
		domain, err := s.Store.GetDomainBySlug(ctx, sp.Domain)
		if err != nil {
			return created, err
		}
		pid, err := uuid.NewV7()
		if err != nil {
			return created, err
		}
		rid, err := uuid.NewV7()
		if err != nil {
			return created, err
		}
		err = s.Store.InTx(ctx, func(q *store.Queries, tx pgx.Tx) error {
			if err := q.SeedCreateProblem(ctx, store.SeedCreateProblemParams{ID: pid, DomainID: domain.ID, AuthorID: authorID}); err != nil {
				return err
			}
			if err := q.SeedCreateRevision(ctx, store.SeedCreateRevisionParams{
				ID: rid, ProblemID: pid, AuthorID: authorID,
				Title: sp.Title, CurrentProcess: sp.CurrentProcess, Pain: sp.Pain, Tried: sp.Tried,
			}); err != nil {
				return err
			}
			if err := q.SeedSetCurrentRevision(ctx, store.SeedSetCurrentRevisionParams{ID: pid, CurrentRevisionID: &rid}); err != nil {
				return err
			}
			_, err := s.Jobs.InsertTx(ctx, tx, jobs.EmbedRevisionArgs{RevisionID: rid}, nil)
			return err
		})
		if err != nil {
			return created, err
		}
		created++
		s.Log.InfoContext(ctx, "seed problem created", "problem_id", pid, "domain", sp.Domain)
	}
	return created, nil
}
