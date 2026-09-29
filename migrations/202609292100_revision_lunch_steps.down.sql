BEGIN;

LOCK TABLE planning.route_lunch_step, planning.route_visit, planning.route_proposal,
    gateway_ops.command_result IN ACCESS EXCLUSIVE MODE;

DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM planning.route_lunch_step)
        OR EXISTS (SELECT 1 FROM planning.route_visit WHERE visit_kind = 'lunch')
        OR EXISTS (SELECT 1 FROM planning.route_proposal WHERE reason = 'lunch')
        OR EXISTS (SELECT 1 FROM planning.route_proposal
                   WHERE candidate::text ~ '"(external_lunch|external_venue|lunch)"')
        OR EXISTS (SELECT 1 FROM gateway_ops.command_result
                   WHERE response_body::text ~ '"(external_lunch|external_venue|lunch)"') THEN
        RAISE EXCEPTION 'cannot remove lunch storage while route or replay data uses it';
    END IF;
END $$;

DROP TABLE planning.route_lunch_step;

ALTER TABLE planning.route_proposal
    DROP CONSTRAINT route_proposal_reason_check,
    ADD CONSTRAINT route_proposal_reason_check
        CHECK (reason IN ('delay', 'cancel', 'delete', 'pin', 'weather'));

ALTER TABLE planning.route_visit
    DROP CONSTRAINT route_visit_visit_kind_check,
    DROP CONSTRAINT route_visit_lunch_neutral_check,
    ADD CONSTRAINT route_visit_visit_kind_check
        CHECK (visit_kind IN ('visit', 'free_time'));

COMMIT;
