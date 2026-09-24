-- 0014_payments — which payment methods a service accepts, as content.
-- Rule: internal/domain/payment (a player pays a charge with cash or with
-- their bank card, one method per charge, chosen by the player). Content:
-- configs/content/payments.yml, and an optional `payment:` narrowing on a
-- course (education.yml, stored inside its document) or a transport mode
-- (transport.yml, the column below). Money still moves only through the
-- ledger of 0006: a card payment is the same transaction under the same
-- reason, taken from player_bank instead of player_cash, so the ledger itself
-- records which method paid.
--
-- content_documents is the general home of content that is stored as the
-- authored document, one set of rows per content version, like crime_content:
-- here the payment services, and the later content types (places, items,
-- shops, elections) under their own kinds, so a new type of content needs no
-- new table.
--
-- Conventions inherited from 0001 onward: instants are timestamptz in UTC;
-- no DEFAULT now(); CHECK constraints are named.

BEGIN;

CREATE TABLE content_documents (
    id                 uuid  PRIMARY KEY,
    content_version_id uuid  NOT NULL REFERENCES content_versions (id),
    -- Which list of which file: payment_service, and the kinds later
    -- migrations add. Open vocabulary; the loader refuses a kind it does
    -- not know.
    kind               text  NOT NULL,
    code               text  NOT NULL,
    -- The entry's place in its list, so an ordered list reads back in order.
    position           int   NOT NULL,
    definition         jsonb NOT NULL,

    CONSTRAINT content_documents_kind_check CHECK (kind ~ '^[a-z][a-z_]*$'),
    CONSTRAINT content_documents_version_kind_code_key UNIQUE (content_version_id, kind, code)
);

CREATE INDEX content_documents_content_version_id_idx ON content_documents (content_version_id);

COMMENT ON TABLE content_documents IS
    'Content stored as authored documents, one set per content version. Validated by the loader before it is written; read back into the same types.';

-- A transport mode may take only some methods for its fare. NULL: whatever
-- the fare service takes (payments.yml).
ALTER TABLE transport_modes ADD COLUMN payment text[] NULL;

COMMENT ON COLUMN transport_modes.payment IS
    'The payment methods (cash, card) this mode''s fare accepts; NULL means whatever payments.yml lets every fare accept.';

COMMIT;
