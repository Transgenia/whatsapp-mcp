package main

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"go.mau.fi/whatsmeow/types"
)

type fakeLIDs map[string]types.JID

func (f fakeLIDs) GetPNForLID(_ context.Context, lid types.JID) (types.JID, error) {
	return f[lid.User], nil
}

func memoryStore(t *testing.T) *MessageStore {
	t.Helper()
	db, err := sql.Open("sqlite3", "file::memory:?_foreign_keys=on")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	if err := initMessageSchema(db); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return &MessageStore{db: db}
}

func mapping(t *testing.T, store *MessageStore) map[string]string {
	t.Helper()
	rows, err := store.db.Query("SELECT lid, pn FROM lid_map")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	got := map[string]string{}
	for rows.Next() {
		var lid, pn string
		if err := rows.Scan(&lid, &pn); err != nil {
			t.Fatal(err)
		}
		got[lid] = pn
	}
	return got
}

func TestLIDPairNeedsOneHiddenAndOnePhone(t *testing.T) {
	lid := types.NewJID("111", types.HiddenUserServer)
	pn := types.NewJID("5215500000101", types.DefaultUserServer)
	if l, p, ok := lidPair(pn, lid); !ok || l != "111@lid" || p != "5215500000101@s.whatsapp.net" {
		t.Fatalf("par invertido: %v %v %v", l, p, ok)
	}
	for _, bad := range [][2]types.JID{{lid, lid}, {pn, pn}, {lid, types.EmptyJID}, {types.NewJID("123", types.GroupServer), pn}} {
		if _, _, ok := lidPair(bad[0], bad[1]); ok {
			t.Fatalf("no debía formar par: %v", bad)
		}
	}
}

func TestRefreshStoresOnlyKnownPairsAndWritesOnce(t *testing.T) {
	store := memoryStore(t)
	now := time.Date(2026, 11, 10, 18, 0, 0, 0, time.UTC)
	for _, chat := range []string{"111@lid", "222@lid", "5215500000101@s.whatsapp.net", "999@g.us"} {
		if err := store.StoreChat(chat, "x", now); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.StoreMessage("m1", "999@g.us", "333@lid", "hola", now, false, "", "", "", nil, nil, nil, 0); err != nil {
		t.Fatal(err)
	}
	resolver := fakeLIDs{"111": types.NewJID("5215500000111", types.DefaultUserServer), "333": types.NewJID("5215500000333", types.DefaultUserServer)}
	n, err := refreshLIDMap(context.Background(), resolver, store, func() time.Time { return now })
	if err != nil || n != 2 {
		t.Fatalf("refresh: n=%d err=%v", n, err)
	}
	want := map[string]string{"111@lid": "5215500000111@s.whatsapp.net", "333@lid": "5215500000333@s.whatsapp.net"}
	if got := mapping(t, store); len(got) != 2 || got["111@lid"] != want["111@lid"] || got["333@lid"] != want["333@lid"] {
		t.Fatalf("mapa: %v", got)
	}
	if n, _ := refreshLIDMap(context.Background(), resolver, store, func() time.Time { return now }); n != 0 {
		t.Fatalf("lo ya anotado no se vuelve a escribir: %d", n)
	}
}

func TestHistoryConversationCarriesItsPair(t *testing.T) {
	store := memoryStore(t)
	if !noteConversationLIDs(store, "555@lid", "5215500000555@s.whatsapp.net", time.Now()) {
		t.Fatal("par del historial no anotado")
	}
	if noteConversationLIDs(store, "", "5215500000555@s.whatsapp.net", time.Now()) {
		t.Fatal("sin lid no hay par")
	}
	if got := mapping(t, store); got["555@lid"] != "5215500000555@s.whatsapp.net" || len(got) != 1 {
		t.Fatalf("mapa: %v", got)
	}
}

func TestMessageCarriesItsOwnPair(t *testing.T) {
	store := memoryStore(t)
	info := types.MessageInfo{MessageSource: types.MessageSource{
		Chat: types.NewJID("444", types.HiddenUserServer), Sender: types.NewJID("444", types.HiddenUserServer),
		SenderAlt: types.NewJID("5215500000444", types.DefaultUserServer),
	}}
	if n := noteMessageLIDs(store, info, time.Now()); n != 1 {
		t.Fatalf("pares anotados: %d", n)
	}
	if got := mapping(t, store); got["444@lid"] != "5215500000444@s.whatsapp.net" {
		t.Fatalf("mapa: %v", got)
	}
}
