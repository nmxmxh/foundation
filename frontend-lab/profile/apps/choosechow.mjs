/**
 * ChooseChow's signed-out landing page (/) as a load-profile target.
 *
 *   LOAD_APP=profile/apps/choosechow.mjs LOAD_DEVICE=small node profile/loadProfile.mjs
 *
 * Builds: snapshots of chowdash_rider_v1/frontend/dist copied under
 * results/apps/choosechow/<name>/ (a fixed baseline, unaffected by rebuilds).
 *
 * The landing page's only anonymous API reads are /v1/discover/dishes and
 * /v1/discover/chefs. Production excludes seed rows from both (an empty
 * catalogue), so these fixtures stand in for a deployment with a real one:
 * the rows of migration 000002 in the service's JSON shape
 * (internal/service/discovery/service.go), with its media served from the
 * repository's uploads/seed — including the three ~950 KB 1024×1024 JPEG
 * photographs, which is what a real catalogue of phone photos looks like.
 */
import { fileURLToPath } from "node:url";

const here = (p) => fileURLToPath(new URL(p, import.meta.url));
const chowdash = here("../../../../chowdash_rider_v1/");

const media = (p) => `/v1/media/objects/seed/${p}`;
const chefs = {
  amara: { id: "22222222-0000-4000-8000-000000000001", name: "Amara's Kitchen", cuisine: "Nigerian & West African", area: "Lekki", avatar: media("chef/amara.png") },
  tunde: { id: "22222222-0000-4000-8000-000000000002", name: "Tunde's Suya & Grill", cuisine: "Suya & BBQ Fusion", area: "Maitama", avatar: media("chef/tunde.png") },
  laila: { id: "22222222-0000-4000-8000-000000000003", name: "Laila's Bakery", cuisine: "Nigerian Pastries & Tea", area: "Garki", avatar: media("chef/laila.png") },
};
const dish = (id, chef, name, category, priceMinor, image) => ({
  id: `44444444-0000-4000-8000-00000000000${id}`,
  name,
  image: media(`dish/${image}`),
  price_minor: priceMinor,
  currency: "NGN",
  category,
  chef: chefs[chef].name,
  chef_id: chefs[chef].id,
  chef_avatar: chefs[chef].avatar,
  rating: 0,
  reviews: 0,
});
const dishes = [
  dish(1, "amara", "Smoky Nigerian Party Jollof", "rice", 450000, "jollof.png"),
  dish(2, "amara", "Special Egusi & Pounded Yam", "soups", 520000, "egusi.png"),
  dish(3, "amara", "Abuja Beef Suya Skewers", "grill", 380000, "suya.png"),
  dish(4, "laila", "Crispy Akara & Ogi Pap", "breakfast", 250000, "akara.png"),
  dish(5, "tunde", "Ofada Rice & Ayamase Sauce", "rice", 420000, "ofada.png"),
  dish(6, "tunde", "Peppered Snapper & Yam Chips", "protein", 600000, "tilapia.png"),
  dish(7, "laila", "Celebration Velvet Cake", "pastry", 1200000, "cake.png"),
  dish(8, "laila", "Nigerian Pastry Box", "pastry", 600000, "pastry.png"),
];
const dishCount = (chefId) => dishes.filter((d) => d.chef_id === chefId).length;

export default {
  name: "choosechow",
  route: "/",
  builds: {
    baseline: here("../../results/apps/choosechow/baseline/"),
    prerender: here("../../results/apps/choosechow/prerender/"),
    "prerender-critical": here("../../results/apps/choosechow/prerender-critical/"),
    "prerender-inline": here("../../results/apps/choosechow/prerender-inline/"),
    "prerender-critical-fonts": here("../../results/apps/choosechow/prerender-critical-fonts/"),
    "prerender-critical-fonts-static": here("../../results/apps/choosechow/prerender-critical-fonts-static/"),
  },
  // The web deployment's nginx sets no COOP/COEP (config/nginx.conf); match it.
  isolation: false,
  fixtures: {
    "/api/v1/discover/dishes": { dishes },
    "/api/v1/discover/chefs": {
      chefs: Object.values(chefs).map((c) => ({ ...c, open: true, rating: 0, reviews: 0, orders: 0, dish_count: dishCount(c.id) })),
    },
  },
  mounts: {
    "/v1/media/objects/seed/": `${chowdash}uploads/seed/`,
  },
  settleMs: 6000,
  // Self-hosted web fonts, for profile/fontFallbackMetrics.mjs (size-adjusted fallbacks).
  fonts: {
    dir: `${chowdash}frontend/public/fonts/`,
    faces: [
      { family: "Plus Jakarta Sans", file: "plus-jakarta-sans-latin-variable.woff2" },
      { family: "Outfit", file: "outfit-latin-variable.woff2" },
      { family: "Instrument Sans", file: "instrument-sans-variable.woff2" },
      { family: "Bricolage Grotesque", file: "bricolage-grotesque-variable.woff2" },
    ],
  },
};
