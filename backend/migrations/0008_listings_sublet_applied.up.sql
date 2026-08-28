-- ECO sublet policy propagation (evidence 2026-08-28): listings that predate
-- the SupportSublet=1 + SubletPricingMethod=2 payload policy must be forced
-- through the reprice pipeline (noise-floor exemption) until confirmed applied.
ALTER TABLE listings ADD COLUMN sublet_applied boolean NOT NULL DEFAULT false;
