/* Fork-only (on8ar, locode): same-origin fetch for static JSON that ships with
 * the UI (locode.json, locode-coords.json). The UI host serves these files in
 * every deployment mode, so they must not go through CSApi, which targets the
 * API host (a 404 in split mode). Exempted from the CSApi lint in
 * .eslintrc.json, like api-base.js (upstream #2383 added that lint). */
(function () {
  'use strict';
  window.MC_fetchStatic = function (path) {
    return fetch(path, { credentials: 'same-origin' });
  };
})();
