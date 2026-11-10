package main

import (
	"context"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/types"
	waLog "go.mau.fi/whatsmeow/util/log"
)

// Equivalencia entre identificador oculto (LID) y teléfono, para el lector de la copia de Aitana.
//
// WhatsApp pasó casi todos los chats a identificadores ocultos (@lid). whatsmeow guarda en la base
// de la sesión a qué teléfono corresponde cada uno cuando WhatsApp lo compartió; aquí se copia esa
// equivalencia a messages.db para que el lector de la copia certificada cruce chats con Odoo sin
// abrir whatsapp.db. Solo se anotan pares que el propio mensaje trae o que la biblioteca ya conoce:
// no hay red ni búsquedas, y un identificador sin teléfono conocido se queda sin par.

const lidMapSchema = `CREATE TABLE IF NOT EXISTS lid_map (
	lid TEXT PRIMARY KEY,
	pn TEXT NOT NULL,
	updated_at TIMESTAMP NOT NULL
);`

// lidRefreshEvery fija cada cuánto se completa la tabla; solo escribe cuando aparece un par nuevo,
// así no rompe la ventana sin escrituras que necesita el refresco de la copia.
const lidRefreshEvery = 15 * time.Minute

type lidResolver interface {
	GetPNForLID(ctx context.Context, lid types.JID) (types.JID, error)
}

// lidPair devuelve el par (lid, pn) normalizado si uno es un identificador oculto y el otro un teléfono.
func lidPair(a, b types.JID) (string, string, bool) {
	a, b = a.ToNonAD(), b.ToNonAD()
	if a.Server == types.DefaultUserServer && b.Server == types.HiddenUserServer {
		a, b = b, a
	}
	if a.Server != types.HiddenUserServer || b.Server != types.DefaultUserServer || a.User == "" || b.User == "" {
		return "", "", false
	}
	return a.String(), b.String(), true
}

func (store *MessageStore) StoreLIDMapping(lid, pn string, at time.Time) error {
	_, err := store.db.Exec(`INSERT INTO lid_map (lid, pn, updated_at) VALUES (?, ?, ?)
		ON CONFLICT(lid) DO UPDATE SET pn = excluded.pn, updated_at = excluded.updated_at
		WHERE lid_map.pn <> excluded.pn`, lid, pn, at)
	return err
}

// unmappedLIDs: identificadores ocultos guardados como chat o remitente que aún no tienen teléfono.
func (store *MessageStore) unmappedLIDs(limit int) ([]string, error) {
	rows, err := store.db.Query(`SELECT jid FROM (
			SELECT jid FROM chats WHERE jid LIKE '%@lid'
			UNION SELECT sender FROM messages WHERE sender LIKE '%@lid'
		) WHERE jid NOT IN (SELECT lid FROM lid_map) LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var lids []string
	for rows.Next() {
		var lid string
		if err := rows.Scan(&lid); err != nil {
			return nil, err
		}
		lids = append(lids, lid)
	}
	return lids, rows.Err()
}

// refreshLIDMap anota el teléfono que la biblioteca ya conoce para cada identificador oculto guardado.
func refreshLIDMap(ctx context.Context, resolver lidResolver, store *MessageStore, now func() time.Time) (int, error) {
	lids, err := store.unmappedLIDs(20000)
	if err != nil {
		return 0, err
	}
	stored := 0
	for _, raw := range lids {
		if err := ctx.Err(); err != nil {
			return stored, err
		}
		lid, err := types.ParseJID(raw)
		if err != nil || lid.Server != types.HiddenUserServer {
			continue
		}
		pn, err := resolver.GetPNForLID(ctx, lid)
		if err != nil || pn.User == "" {
			continue
		}
		if l, p, ok := lidPair(lid, pn); ok {
			if err := store.StoreLIDMapping(l, p, now()); err != nil {
				return stored, err
			}
			stored++
		}
	}
	return stored, nil
}

// keepLIDMap completa la tabla al autenticar y luego cada lidRefreshEvery. Solo registra conteos.
func keepLIDMap(ctx context.Context, client *whatsmeow.Client, store *MessageStore, logger waLog.Logger) {
	ticker := time.NewTicker(lidRefreshEvery)
	defer ticker.Stop()
	for {
		if n, err := refreshLIDMap(ctx, client.Store.LIDs, store, time.Now); err != nil {
			if ctx.Err() != nil {
				return
			}
			logger.Warnf("lid_map=refresh_failed")
		} else if n > 0 {
			logger.Infof("lid_map=refreshed pairs=%d", n)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// noteConversationLIDs anota el par que trae una conversación del historial (lidJID y pnJID).
func noteConversationLIDs(store *MessageStore, lidJID, pnJID string, at time.Time) bool {
	lid, errL := types.ParseJID(lidJID)
	pn, errP := types.ParseJID(pnJID)
	if errL != nil || errP != nil {
		return false
	}
	l, p, ok := lidPair(lid, pn)
	return ok && store.StoreLIDMapping(l, p, at) == nil
}

// noteMessageLIDs anota los pares que trae el propio mensaje: el remitente con su dirección alterna y,
// en un chat directo, el chat con la dirección alterna del destinatario.
func noteMessageLIDs(store *MessageStore, info types.MessageInfo, at time.Time) int {
	stored := 0
	for _, pair := range [][2]types.JID{{info.Sender, info.SenderAlt}, {info.Chat, info.RecipientAlt}} {
		if l, p, ok := lidPair(pair[0], pair[1]); ok && store.StoreLIDMapping(l, p, at) == nil {
			stored++
		}
	}
	return stored
}
