-- The morning delivery counted only the units that fit as delivered while the
-- units the shelf's cap turned away went to trimmed_total, so
-- stock = delivered_total - sold_total - trimmed_total did not hold on any line
-- that was ever trimmed. Every line's delivered total is what arrived: what is
-- on the shelf, what was sold and what was turned away.
UPDATE village_shop_lines
   SET delivered_total = stock + sold_total + trimmed_total
 WHERE delivered_total <> stock + sold_total + trimmed_total;
