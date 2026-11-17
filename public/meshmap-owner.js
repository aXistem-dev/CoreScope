/* Fork-local (meshmap owner): the Discord username of a repeater's owner on
 * meshmap.radio-actief.be, as an extra row in the Repeater info section.
 * One fetch of /api/meshmap/owners per page load (the server caches the
 * meshmap list); rowHtml returns '' when the repeater has no known owner or
 * the feature is off. Every dynamic value goes through escapeHtml. */
(function () {
  'use strict';
  var byKey = {};
  var source = '';
  var loading = null;

  function ready() {
    if (!loading) {
      loading = Promise.resolve().then(function () {
        return window.CSApi.fetch('/api/meshmap/owners');
      }).then(function (r) {
        return r && r.ok ? r.json() : null;
      }).then(function (d) {
        if (!d || !Array.isArray(d.owners)) return;
        source = String(d.source || '');
        d.owners.forEach(function (o) {
          if (o && o.pubkey && o.discordName) byKey[String(o.pubkey).toLowerCase()] = String(o.discordName);
        });
      }, function () {});
    }
    return loading;
  }

  function linkFor(base, node) {
    var pk = String(node.public_key || '').toLowerCase();
    var lat = Number(node.lat), lon = Number(node.lon);
    var hasPos = node.lat != null && node.lon != null && isFinite(lat) && isFinite(lon) && !(lat === 0 && lon === 0);
    return base + '/?' + (hasPos ? 'zoom=15&lat=' + lat + '&lon=' + lon + '&' : '') + 'node=' + pk;
  }

  function rowHtml(node) {
    var name = node && byKey[String(node.public_key || '').toLowerCase()];
    if (!name || !source) return '';
    return '<div class="repeater-info-row"><dt>Owner (Discord)</dt><dd>@' + escapeHtml(name) +
      ' · <a href="' + escapeHtml(linkFor(source, node)) + '" target="_blank" rel="noopener noreferrer">view on meshmap</a></dd></div>';
  }

  window.CSMeshmapOwner = { ready: ready, rowHtml: rowHtml, _test: { linkFor: linkFor } };
})();
