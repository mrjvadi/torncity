-- 0132_spawn_circles - the state of the spawn circles (docs/adr/0028 section 3.2, amendment 2026-10-09).
--
-- New settlements are founded close together: one current circle (a centre and a radius on the sphere) is filled
-- with foundings, then the next circle opens beside the filled ones along a hex spiral. The circles are stored so
-- the spiral never changes under a running world (the centre of circle k is written when it opens), and one locked
-- row per world, the cursor, serialises foundings: two foundings at the same moment never take the same cell or
-- the same slot.
--
-- Conventions: instants are timestamptz in UTC; CHECK constraints are named; a world's circles go with the world.

BEGIN;

CREATE TABLE spawn_circles (
    world_id     uuid             NOT NULL REFERENCES worlds (id) ON DELETE CASCADE,
    idx          int              NOT NULL,
    lat_deg      double precision NOT NULL,
    lon_deg      double precision NOT NULL,
    radius_km    double precision NOT NULL,
    capacity     int              NOT NULL,
    taken        int              NOT NULL DEFAULT 0,
    opened_at    timestamptz      NOT NULL,
    closed_at    timestamptz      NULL,
    close_reason text             NULL,

    PRIMARY KEY (world_id, idx),
    CONSTRAINT spawn_circles_idx_check CHECK (idx >= 0),
    CONSTRAINT spawn_circles_radius_check CHECK (radius_km > 0 AND capacity > 0 AND taken >= 0),
    CONSTRAINT spawn_circles_close_check CHECK ((closed_at IS NULL) = (close_reason IS NULL)),
    CONSTRAINT spawn_circles_reason_check CHECK (close_reason IS NULL OR close_reason IN ('full', 'no_room'))
);

CREATE TABLE spawn_circle_cursor (
    world_id    uuid PRIMARY KEY REFERENCES worlds (id) ON DELETE CASCADE,
    current_idx int  NULL
);

COMMENT ON TABLE spawn_circles IS
    'One row per spawn circle, written when it opens (ADR 0028 section 3.2 amendment). taken counts the settlements standing inside when it opened plus the foundings placed in it since.';
COMMENT ON TABLE spawn_circle_cursor IS
    'The row every founding on a world takes FOR UPDATE: the current circle. current_idx is NULL until the first founding.';

COMMIT;
