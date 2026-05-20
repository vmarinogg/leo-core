package gardener

import (
	"encoding/json"
	"fmt"
	"os"
)

// WriteGraphHTML generates a self-contained HTML file with an interactive
// D3.js force-directed graph visualization of the memory graph.
func WriteGraphHTML(data *GraphData, outPath string) error {
	jsonData, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("marshaling graph data: %w", err)
	}

	html := fmt.Sprintf(graphHTMLTemplate, string(jsonData))
	return os.WriteFile(outPath, []byte(html), 0644)
}

// graphHTMLTemplate is the HTML template with a single %s placeholder for the JSON data.
// It uses double-quoted strings in JS to avoid backtick issues with Go raw strings.
const graphHTMLTemplate = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>MOM Memory Graph</title>
<style>
* { margin: 0; padding: 0; box-sizing: border-box; }
body { font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif; background: #001423; color: #FFF5E5; overflow: hidden; }
#container { width: 100vw; height: 100vh; position: relative; }
svg { width: 100%%; height: 100%%; }
.stats { position: absolute; top: 16px; left: 16px; background: rgba(0,20,35,0.92); border: 1px solid #3B1F0A; border-radius: 8px; padding: 16px; font-size: 13px; line-height: 1.6; z-index: 10; }
.stats h2 { font-size: 15px; margin-bottom: 8px; color: #0066B1; }
.stats span { color: #8b949e; }
.stats .value { color: #FFF5E5; font-weight: 600; }
.legend { position: absolute; bottom: 16px; left: 16px; background: rgba(0,20,35,0.92); border: 1px solid #3B1F0A; border-radius: 8px; padding: 12px 16px; font-size: 12px; z-index: 10; }
.legend-item { display: flex; align-items: center; gap: 8px; margin: 4px 0; cursor: pointer; padding: 2px 6px; border-radius: 4px; transition: opacity 0.15s; user-select: none; }
.legend-item:hover { background: rgba(255,245,229,0.06); }
.legend-item.inactive { opacity: 0.3; }
.legend-dot { width: 12px; height: 12px; border-radius: 50%%; }
.tooltip { position: absolute; background: rgba(0,20,35,0.95); border: 1px solid #3B1F0A; border-radius: 6px; padding: 12px; font-size: 12px; pointer-events: none; opacity: 0; transition: opacity 0.15s; z-index: 20; max-width: 350px; }
.tooltip h3 { color: #0066B1; margin-bottom: 4px; font-size: 13px; }
.tooltip .tags { color: #8b949e; margin-top: 4px; }
.controls { position: absolute; top: 16px; right: 16px; background: rgba(0,20,35,0.92); border: 1px solid #3B1F0A; border-radius: 8px; padding: 12px; z-index: 10; }
.controls label { display: block; margin: 4px 0; font-size: 12px; cursor: pointer; }
.controls input[type="checkbox"] { margin-right: 6px; }
.controls input[type="range"] { width: 120px; }
</style>
</head>
<body>
<div id="container">
<svg id="graph"></svg>
<div class="stats">
  <h2>MOM Memory Graph</h2>
  <div><span>Documents:</span> <span class="value" id="stat-docs"></span></div>
  <div><span>Edges:</span> <span class="value" id="stat-edges"></span></div>
  <div><span>Landmarks:</span> <span class="value" id="stat-landmarks"></span></div>
  <div><span>Tags:</span> <span class="value" id="stat-tags"></span></div>
</div>
<div class="legend" id="legend">
  <div class="legend-item active" data-filter="landmark"><div class="legend-dot" style="background:#FFCC2C"></div> Landmark</div>
  <div class="legend-item active" data-filter="pattern"><div class="legend-dot" style="background:#0066B1"></div> pattern</div>
  <div class="legend-item active" data-filter="decision"><div class="legend-dot" style="background:#4d9fd4"></div> decision</div>
  <div class="legend-item active" data-filter="learning"><div class="legend-dot" style="background:#3B1F0A; border: 1px solid #8B6914"></div> learning</div>
  <div class="legend-item active" data-filter="fact"><div class="legend-dot" style="background:#8B6914"></div> fact</div>
  <div class="legend-item active" data-filter="feedback"><div class="legend-dot" style="background:#c9943a"></div> feedback</div>
  <div class="legend-item active" data-filter="other"><div class="legend-dot" style="background:#5a7a8a"></div> other</div>
</div>
<div class="controls">
  <label><input type="checkbox" id="show-labels" /> Show labels</label>
  <label>Edge threshold <input type="range" id="edge-threshold" min="0" max="1" step="0.01" value="0" /></label>
</div>
<div class="tooltip" id="tooltip"></div>
</div>

<script src="https://d3js.org/d3.v7.min.js"></script>
<script>
var data = %s;

document.getElementById('stat-docs').textContent = data.stats.total_docs;
document.getElementById('stat-edges').textContent = data.stats.total_edges;
document.getElementById('stat-landmarks').textContent = data.stats.landmark_count;
document.getElementById('stat-tags').textContent = data.stats.tag_count;

var width = window.innerWidth;
var height = window.innerHeight;
var svg = d3.select('#graph');
var g = svg.append('g');
var tooltip = document.getElementById('tooltip');

// Zoom
svg.call(d3.zoom()
  .scaleExtent([0.1, 8])
  .on('zoom', function(e) { g.attr('transform', e.transform); }));

// Color by type
function nodeColor(d) {
  if (d.landmark) return '#FFCC2C';   // Archive — highlight/poster
  switch (d.type) {
    case 'pattern':  return '#0066B1'; // Signal — primary action
    case 'decision': return '#4d9fd4'; // Signal 300 — lighter blue
    case 'learning': return '#3B1F0A'; // Walnut — warm dark
    case 'fact':     return '#8B6914'; // Archive 700 — deep gold
    case 'feedback': return '#c9943a'; // Archive 400 — warm mid
    default:         return '#5a7a8a'; // Ink 400 — muted slate
  }
}

// Node radius by edge count — log scale to amplify differences.
// Without log, a doc with 800 edges vs 750 looks identical.
// With log, the range spreads across small and large counts.
var maxEdges = d3.max(data.nodes, function(d) { return d.edge_count; }) || 1;
var logMax = Math.log(maxEdges + 1);
function nodeRadius(d) {
  var logVal = Math.log(d.edge_count + 1);
  var ratio = logVal / logMax;
  if (d.landmark) return 8 + ratio * 16;
  return 2 + ratio * 10;
}

// Build simulation
var simulation = d3.forceSimulation(data.nodes)
  .force('link', d3.forceLink(data.edges).id(function(d) { return d.id; }).distance(80).strength(function(d) { return d.weight * 0.2; }))
  .force('charge', d3.forceManyBody().strength(-50))
  .force('center', d3.forceCenter(width / 2, height / 2))
  .force('collision', d3.forceCollide().radius(function(d) { return nodeRadius(d) + 3; }));

// Draw edges — visible lines with weight-based thickness.
var maxWeight = d3.max(data.edges, function(d) { return d.weight; }) || 1;
var link = g.append('g')
  .selectAll('line')
  .data(data.edges)
  .join('line')
  .attr('stroke', '#1a4a6a')
  .attr('stroke-width', function(d) { return 0.5 + (d.weight / maxWeight) * 2.5; })
  .attr('stroke-opacity', 0.6);

// Draw nodes
var node = g.append('g')
  .selectAll('circle')
  .data(data.nodes)
  .join('circle')
  .attr('r', nodeRadius)
  .attr('fill', nodeColor)
  .attr('stroke', function(d) { return d.landmark ? '#FFCC2C' : 'rgba(255,245,229,0.12)'; })
  .attr('stroke-width', function(d) { return d.landmark ? 3 : 0.5; })
  .attr('cursor', 'pointer')
  .call(d3.drag()
    .on('start', function(e, d) { if (!e.active) simulation.alphaTarget(0.3).restart(); d.fx = d.x; d.fy = d.y; })
    .on('drag', function(e, d) { d.fx = e.x; d.fy = e.y; })
    .on('end', function(e, d) { if (!e.active) simulation.alphaTarget(0); d.fx = null; d.fy = null; }));

// Labels (hidden by default)
var label = g.append('g')
  .selectAll('text')
  .data(data.nodes)
  .join('text')
  .text(function(d) {
    if (d.summary && d.summary.length > 30) return d.summary.substring(0, 30) + '...';
    return d.summary || d.id;
  })
  .attr('font-size', 9)
  .attr('fill', '#8b949e')
  .attr('dx', 8)
  .attr('dy', 3)
  .attr('display', 'none');

// Tooltip
node.on('mouseover', function(e, d) {
  var tags = d.tags ? d.tags.join(', ') : '';
  tooltip.innerHTML = '<h3>' + d.id + '</h3>' +
    '<div>' + (d.summary || '') + '</div>' +
    '<div>Type: ' + d.type + ' | Edges: ' + d.edge_count + ' | Centrality: ' + d.score.toFixed(4) + (d.landmark ? ' | \u2605 Landmark' : '') + '</div>' +
    '<div class="tags">Tags: ' + tags + '</div>';
  tooltip.style.opacity = 1;
  tooltip.style.left = (e.pageX + 12) + 'px';
  tooltip.style.top = (e.pageY - 12) + 'px';
}).on('mousemove', function(e) {
  tooltip.style.left = (e.pageX + 12) + 'px';
  tooltip.style.top = (e.pageY - 12) + 'px';
}).on('mouseout', function() {
  tooltip.style.opacity = 0;
});

// Tick
simulation.on('tick', function() {
  link.attr('x1', function(d) { return d.source.x; }).attr('y1', function(d) { return d.source.y; })
      .attr('x2', function(d) { return d.target.x; }).attr('y2', function(d) { return d.target.y; });
  node.attr('cx', function(d) { return d.x; }).attr('cy', function(d) { return d.y; });
  label.attr('x', function(d) { return d.x; }).attr('y', function(d) { return d.y; });
});

// Controls
document.getElementById('show-labels').addEventListener('change', function() {
  applyFilters();
});

// Legend filter state: which categories are visible.
var activeFilters = { landmark: true, pattern: true, decision: true, learning: true, fact: true, feedback: true, other: true };

function nodeCategory(d) {
  if (d.landmark) return 'landmark';
  if (['pattern','decision','learning','fact','feedback'].indexOf(d.type) >= 0) return d.type;
  return 'other';
}

function isNodeVisible(d) {
  // A node is visible if ANY of its matching categories is active.
  // Landmark nodes match both 'landmark' and their type.
  if (d.landmark && activeFilters.landmark) return true;
  var cat = (['pattern','decision','learning','fact','feedback'].indexOf(d.type) >= 0) ? d.type : 'other';
  return activeFilters[cat];
}

function applyFilters() {
  var threshold = parseFloat(document.getElementById('edge-threshold').value);
  var showLabels = document.getElementById('show-labels').checked;

  node.attr('display', function(d) { return isNodeVisible(d) ? null : 'none'; });
  label.attr('display', function(d) {
    if (!showLabels) return 'none';
    return isNodeVisible(d) ? null : 'none';
  });
  link.attr('display', function(d) {
    if (d.weight < threshold) return 'none';
    var src = typeof d.source === 'object' ? d.source : data.nodes.find(function(n) { return n.id === d.source; });
    var tgt = typeof d.target === 'object' ? d.target : data.nodes.find(function(n) { return n.id === d.target; });
    if (!src || !tgt) return 'none';
    return (isNodeVisible(src) && isNodeVisible(tgt)) ? null : 'none';
  });
}

// Click legend items to toggle filters.
document.querySelectorAll('.legend-item[data-filter]').forEach(function(el) {
  el.addEventListener('click', function() {
    var key = el.getAttribute('data-filter');
    activeFilters[key] = !activeFilters[key];
    el.classList.toggle('inactive', !activeFilters[key]);
    el.classList.toggle('active', activeFilters[key]);
    applyFilters();
  });
});

document.getElementById('edge-threshold').addEventListener('input', function() {
  applyFilters();
});
</script>
</body>
</html>`
