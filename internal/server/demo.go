package server

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/abjelosevic88/pocket-homelab-documents/internal/store"
)

// seedDemo fills an empty database with a few text documents so the UI has
// something to show (DEMO_DATA=true). Without OCR tools they still get text.
func (s *Server) seedDemo(ctx context.Context) error {
	n, _ := s.store.CountUsers(ctx)
	var u *store.User
	if n == 0 {
		var err error
		if u, err = s.firstOrSystemUser(ctx, "admin@localhost", "Admin"); err != nil {
			return err
		}
	} else {
		users, _ := s.store.ListUsers(ctx)
		u = &users[0]
	}
	if ids := s.store.DocumentIDs(ctx, u.ID, false); len(ids) > 0 {
		return nil
	}
	s.seedDefaults(ctx, u.ID)
	tag := func(name, color string, alg int, m string) int64 {
		t := &store.Tag{UserID: u.ID, Name: name, Color: color, Matchable: store.Matchable{MatchingAlgorithm: alg, Match: m, IsInsensitive: true}}
		if err := s.store.CreateTag(ctx, t); err != nil {
			if ex, err := s.store.FindTag(ctx, u.ID, name); err == nil {
				return ex.ID
			}
		}
		return t.ID
	}
	named := func(create func(context.Context, *store.Named) error, name, m string, alg int) int64 {
		x := &store.Named{UserID: u.ID, Name: name, Matchable: store.Matchable{MatchingAlgorithm: alg, Match: m, IsInsensitive: true}}
		_ = create(ctx, x)
		return x.ID
	}
	tax := tag("Tax", "#1f78b4", store.MatchAny, "tax steuer porez")
	home := tag("Home", "#33a02c", store.MatchAuto, "")
	car := tag("Car", "#ff7f00", store.MatchAny, "vehicle car registration")
	cEnergy := named(s.store.CreateCorrespondent, "Elektroprivreda", "elektroprivreda", store.MatchAny)
	cLandlord := named(s.store.CreateCorrespondent, "Landlord Ltd", "landlord", store.MatchAny)
	cInsurer := named(s.store.CreateCorrespondent, "Sava Insurance", "sava insurance", store.MatchAll)
	tInvoice := named(s.store.CreateDocumentType, "Invoice", "invoice račun rechnung", store.MatchAny)
	tContract := named(s.store.CreateDocumentType, "Contract", "contract ugovor vertrag", store.MatchAny)
	tLetter := named(s.store.CreateDocumentType, "Letter", "", store.MatchAuto)
	_ = tLetter
	byYear := &store.Named{UserID: u.ID, Name: "By year and correspondent", Path: "{created_year}/{correspondent}/{created} {title}"}
	_ = s.store.CreateStoragePath(ctx, byYear)
	fAmount := &store.CustomField{UserID: u.ID, Name: "Amount", DataType: "monetary", Extra: []byte(`{"currency":"EUR"}`)}
	_ = s.store.CreateCustomField(ctx, fAmount)
	fDue := &store.CustomField{UserID: u.ID, Name: "Due date", DataType: "date", Extra: []byte(`{}`)}
	_ = s.store.CreateCustomField(ctx, fDue)

	type demo struct {
		title, created, text string
		tags                 []int64
		corr, typ            int64
		amount               string
	}
	docs := []demo{
		{"Electricity invoice March 2026", "2026-03-14", "ELEKTROPRIVREDA\nInvoice no. 2026-03-1187\nDate: 14.03.2026\nBilling period 01.02.2026 – 28.02.2026\nConsumption 412 kWh\nAmount due: 58,40 EUR\nDue date 31.03.2026\nThank you for paying on time.", []int64{home}, cEnergy, tInvoice, "58.40"},
		{"Electricity invoice April 2026", "2026-04-15", "ELEKTROPRIVREDA\nInvoice no. 2026-04-2231\nDate: 15.04.2026\nBilling period 01.03.2026 – 31.03.2026\nConsumption 377 kWh\nAmount due: 53,10 EUR\nDue date 30.04.2026", []int64{home}, cEnergy, tInvoice, "53.10"},
		{"Apartment lease contract", "2025-09-01", "LEASE CONTRACT\nBetween Landlord Ltd and the tenant.\nAddress: Zmaj Ognjenog Vuka 238, Banja Luka\nMonthly rent 450 EUR, deposit 900 EUR\nStart 01.09.2025, notice period 3 months.\nSigned in Banja Luka, 1 September 2025", []int64{home}, cLandlord, tContract, ""},
		{"Car insurance policy 2026", "2026-01-10", "SAVA INSURANCE\nMotor vehicle insurance policy no. MV-88213\nVehicle: Škoda Fabia, registration BL-123-AB\nPeriod 10.01.2026 – 09.01.2027\nPremium 312,00 EUR paid in full", []int64{car}, cInsurer, tContract, "312.00"},
		{"Annual tax statement 2025", "2026-02-20", "TAX ADMINISTRATION\nAnnual income tax statement for 2025\nTaxpayer: Aleksandar\nTotal income, deductions and tax paid are listed below.\nPorez na dohodak: 1.240,00 KM\nIssued 20.02.2026", []int64{tax}, 0, 0, ""},
		{"Rent receipt October 2025", "2025-10-03", "Landlord Ltd\nReceipt for rent October 2025\nReceived 450 EUR on 03.10.2025\nThank you.", []int64{home}, cLandlord, tInvoice, "450.00"},
	}
	for _, d := range docs {
		tmp := filepath.Join(s.cfg.TmpDir, "demo.txt")
		_ = os.MkdirAll(s.cfg.TmpDir, 0o755)
		if err := os.WriteFile(tmp, []byte(d.text), 0o644); err != nil {
			return err
		}
		in := incoming{Path: tmp, Name: strings.ToLower(strings.ReplaceAll(d.title, " ", "-")) + ".txt", Title: d.title, Created: d.created, Tags: d.tags, Source: "import", Actor: "demo", SkipMatching: true, Content: d.text}
		if d.corr > 0 {
			c := d.corr
			in.Correspondent = &c
		}
		if d.typ > 0 {
			t := d.typ
			in.DocumentType = &t
		}
		if d.amount != "" {
			in.Fields = append(in.Fields, store.FieldValue{FieldID: fAmount.ID, Value: d.amount})
		}
		doc, err := s.ingest(ctx, u.ID, in)
		if err != nil {
			return fmt.Errorf("demo %q: %w", d.title, err)
		}
		_ = s.store.AddNote(ctx, &store.Note{DocumentID: doc.ID, UserID: u.ID, Text: "Demo document."})
	}
	s.log.Info("demo data seeded", "documents", len(docs))
	return nil
}
