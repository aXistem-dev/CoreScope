/* Fork-local (meshmap owner): who claimed a repeater on meshmap.radio-actief.be
 * (owner and managers), as extra rows in the Repeater info section. One fetch
 * of /api/meshmap/owners per page load (the server caches the map's partner
 * claims feed); a failed fetch is retried on the next mount. rowHtml returns
 * '' when nobody claimed the repeater or the feature is off. A person who
 * hides their name shows as "claimed". Every dynamic value goes through
 * escapeHtml. */
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
        // No usable list yet (fetch failed, or the server has not fetched
        // meshmap yet): let the next mount try again instead of hiding the
        // owners for the rest of the session.
        if (!d || !Array.isArray(d.nodes) || !d.fetchedAt) { loading = null; return; }
        source = String(d.source || '');
        var next = {};
        d.nodes.forEach(function (n) {
          if (n && n.pubkey) next[String(n.pubkey).toLowerCase()] = n;
        });
        byKey = next;
      }, function () { loading = null; });
    }
    return loading;
  }

  function linkFor(base, node) {
    var pk = String(node.public_key || '').toLowerCase();
    var lat = Number(node.lat), lon = Number(node.lon);
    var hasPos = node.lat != null && node.lon != null && isFinite(lat) && isFinite(lon) && !(lat === 0 && lon === 0);
    return base + '/?' + (hasPos ? 'zoom=15&lat=' + lat + '&lon=' + lon + '&' : '') + 'node=' + pk;
  }

  function row(label, value) {
    return '<div class="repeater-info-row"><dt>' + label + '</dt><dd>' + value + '</dd></div>';
  }

  function managersText(managers) {
    var named = [], hidden = 0;
    managers.forEach(function (m) {
      var name = m && m.name ? String(m.name) : '';
      if (name) named.push('@' + escapeHtml(name)); else hidden++;
    });
    if (hidden) named.push(hidden + ' with a hidden name');
    return named.join(', ');
  }

  function rowHtml(node) {
    var c = node && byKey[String(node.public_key || '').toLowerCase()];
    if (!c || !source) return '';
    var managers = Array.isArray(c.managers) ? c.managers : [];
    if (!c.owner && !managers.length) return '';
    var link = ' · <a href="' + escapeHtml(linkFor(source, node)) +
      '" target="_blank" rel="noopener noreferrer">view on meshmap</a>';
    var html = '';
    if (c.owner) {
      html += row('Owner', (c.owner.name ? '@' + escapeHtml(String(c.owner.name)) : 'claimed') + link);
    }
    if (managers.length) html += row('Managers', managersText(managers) + (c.owner ? '' : link));
    return html;
  }

  window.CSMeshmapOwner = { ready: ready, rowHtml: rowHtml, _test: { linkFor: linkFor } };
})();
