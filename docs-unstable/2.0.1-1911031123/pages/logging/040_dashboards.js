<doc-view>

<v-layout row wrap>
<v-flex xs12 sm10 lg10>
<v-card class="section-def" v-bind:color="$store.state.currentColor">
<v-card-text class="pa-3">
<v-card class="section-def__card">
<v-card-text>
<dl>
<dt slot=title>Kibana Dashboards</dt>
<dd slot="desc"><p>The Coherence Operator provides a number of Kibana dashboards and querires
to allow you to view and analyze logs from your Coherence clusters.</p>
</dd>
</dl>
</v-card-text>
</v-card>
</v-card-text>
</v-card>
</v-flex>
</v-layout>

<h2 id="_kibana_dashboards_searches">Kibana Dashboards &amp; Searches</h2>
<div class="section">

</div>

<h2 id="_table_of_contents">Table of Contents</h2>
<div class="section">
<ol style="margin-left: 15px;">
<li>
<router-link to="#dashboards" @click.native="this.scrollFix('#dashboards')">Dashboards</router-link>
<ol style="margin-left: 15px;">
<li>
<router-link to="#all" @click.native="this.scrollFix('#all')">Coherence Cluster - All Messages</router-link>

</li>
<li>
<router-link to="#errors" @click.native="this.scrollFix('#errors')">Coherence Cluster - Errors and Warnings</router-link>

</li>
<li>
<router-link to="#persistence" @click.native="this.scrollFix('#persistence')">Coherence Cluster - Persistence</router-link>

</li>
<li>
<router-link to="#config" @click.native="this.scrollFix('#config')">Coherence Cluster - Configuration Messages</router-link>

</li>
<li>
<router-link to="#network" @click.native="this.scrollFix('#network')">Coherence Cluster - Network</router-link>

</li>
<li>
<router-link to="#partitions" @click.native="this.scrollFix('#partitions')">Coherence Cluster - Partitions</router-link>

</li>
<li>
<router-link to="#sources" @click.native="this.scrollFix('#sources')">Coherence Cluster - Message Sources</router-link>

</li>
</ol>
</li>
<li>
<router-link to="#searches" @click.native="this.scrollFix('#searches')">Searches</router-link>

</li>
</ol>
</div>

<h2 id="dashboards">Dashboards</h2>
<div class="section">
<p>Information from all dashboards (and queries) can be filtered using the standard Kibana date/time
filtering in the top right of the UI, as well as the <code>Add a filter</code> button.</p>



<v-card>
<v-card-text class="overflow-y-hidden" style="text-align:center">
<img src="./images/kibana-filters.png" alt="Filters"width="600" />
</v-card-text>
</v-card>


<h3 id="all">1. Coherence Cluster - All Messages</h3>
<div class="section">
<p>This dashboard shows all messages captured for the given time period for all clusters.</p>

<p>Users can drill-down by cluster, host, message level and thread.</p>



<v-card>
<v-card-text class="overflow-y-hidden" style="text-align:center">
<img src="./images/kibana-all-messages.png" alt="All messages"width="900" />
</v-card-text>
</v-card>

</div>

<h3 id="errors">2. Coherence Cluster - Errors and Warnings</h3>
<div class="section">
<p>This dashboard shows errors and warning messages only.</p>

<p>Users can drill-down by cluster, host, message level and thread.</p>



<v-card>
<v-card-text class="overflow-y-hidden" style="text-align:center">
<img src="./images/kibana-errors-warnings.png" alt="Errors and Warnings"width="900" />
</v-card-text>
</v-card>

</div>

<h3 id="persistence">3. Coherence Cluster - Persistence</h3>
<div class="section">
<p>This dashboard shows Persistence related messages including failed and successful operations.</p>



<v-card>
<v-card-text class="overflow-y-hidden" style="text-align:center">
<img src="./images/kibana-persistence.png" alt="Persistence"width="900" />
</v-card-text>
</v-card>

</div>

<h3 id="config">4. Coherence Cluster - Configuration Messages</h3>
<div class="section">
<p>This dashboard shows configuration related messages such as loading of operational, cache configuration
and POF configuration files.</p>



<v-card>
<v-card-text class="overflow-y-hidden" style="text-align:center">
<img src="./images/kibana-configuration.png" alt="Configuration"width="900" />
</v-card-text>
</v-card>

</div>

<h3 id="network">5. Coherence Cluster - Network</h3>
<div class="section">
<p>This dashboard hows network related messages, such as communication delays and TCP ring disconnects.</p>



<v-card>
<v-card-text class="overflow-y-hidden" style="text-align:center">
<img src="./images/kibana-network.png" alt="Network"width="900" />
</v-card-text>
</v-card>

</div>

<h3 id="partitions">6. Coherence Cluster - Partitions</h3>
<div class="section">
<p>Shows partition transfer and partition loss messages.</p>



<v-card>
<v-card-text class="overflow-y-hidden" style="text-align:center">
<img src="./images/kibana-partitions.png" alt="Partitions"width="900" />
</v-card-text>
</v-card>

</div>

<h3 id="sources">7. Coherence Cluster - Message Sources</h3>
<div class="section">
<p>Shows the source (thread) for messages</p>

<p>Users can drill-down by cluster, host and message level.</p>



<v-card>
<v-card-text class="overflow-y-hidden" style="text-align:center">
<img src="./images/kibana-message-sources.png" alt="Sources"width="900" />
</v-card-text>
</v-card>

</div>
</div>

<h2 id="searches">Searches</h2>
<div class="section">
<p>A number of searches are automatically includes which can help assist in
diagnosis and troubleshooting a Coherence cluster. They can be accessed via the <code>Discover</code> <code>side-bar
and selecting `Open</code>.</p>



<v-card>
<v-card-text class="overflow-y-hidden" style="text-align:center">
<img src="./images/kibana-search.png" alt="Search"width="700" />
</v-card-text>
</v-card>

<p>These are grouped into the following general categories:</p>

<ul class="ulist">
<li>
<p>Cluster - Cluster join, discovery, heartbeat, member joining and stopping messages</p>

</li>
<li>
<p>Cache - Cache restarting, exceptions and index exception messages</p>

</li>
<li>
<p>Configuration - Configuration loading and not loading messages</p>

</li>
<li>
<p>Persistence - Persistence success and failure messages</p>

</li>
<li>
<p>Network - Network communications delays, disconnects, timeouts and terminations</p>

</li>
<li>
<p>Partition - Partition loss, ownership and transfer related messages</p>

</li>
<li>
<p>Member - Member thread dump, join and leave messages</p>

</li>
<li>
<p>Errors - All Error messages only</p>

</li>
<li>
<p>Federation - Federation participant, disconnection, connection, errors and other messages</p>

</li>
</ul>
</div>
</doc-view>
