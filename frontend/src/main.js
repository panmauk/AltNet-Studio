import './style.css';
import './app.css';

import {
    Signup, Verify, Login, Logout, CurrentUser, BackendURL,
    RequestPasswordReset, ConfirmPasswordReset,
    EnableNodeMode, DisableNodeMode, IsNodeMode, GetNodeStatus,
    RequestDomain, MyDomains, AdminPending, AdminDecide,
    PickDirectory, PickFiles, PublishSite, PublishToOwnedDomain, OpenURL,
    HostsEntryStatus, InstallHostsEntry, RemoveHostsEntry,
    SiteAnalyticsBatch, TakeDownSite, GatewayAddr, GatewayTLSAddr,
    HTTPSReady, CATrusted, EnableHTTPS, DisableHTTPS,
    GetSeedPeers, SetSeedPeers,
    DeleteAccount,
    GetDefaultSeedPeers,
    HostingTermsAccepted, AcceptHostingTerms,
    ReportSite, AdminReports, AdminDecideReport,
    NeedsRoot, AdminTakedown,
} from '../wailsjs/go/main/App';

// ── star field, generated once ──

(function spawnStars() {
    const layer = document.getElementById('stars');
    if (!layer) return;
    const count = 110;
    for (let i = 0; i < count; i++) {
        const s = document.createElement('div');
        s.className = 'star';
        const size = 1 + Math.random() * 2.2;
        s.style.width  = size + 'px';
        s.style.height = size + 'px';
        s.style.left = (Math.random() * 100) + '%';
        s.style.top  = (Math.random() * 100) + '%';
        s.style.animationDelay    = (Math.random() * 4) + 's';
        s.style.animationDuration = (3 + Math.random() * 4) + 's';
        layer.appendChild(s);
    }
})();

// ── state + router ──

const state = {
    view: 'loading',
    user: null,             // {email, is_admin}
    pendingEmail: '',       // signup email carried through verify
    pendingResetEmail: '',  // email carried through reset flow
    pendingAction: null,    // 'register' | 'admin'
    registeredSites: [],    // approved domain rows; gates the home third card
    hostsInstalled: {},     // name -> bool, whether the clean URL works on this PC
    siteAnalytics: {},      // name -> {requests, bytes, unique_ips, last_seen_unix}
    gatewayAddr: '127.0.0.1:9080',     // HTTP listen address as reported by daemon
    gatewayTLSAddr: '',                // HTTPS listen address; empty = HTTPS off
    httpsReady: false,                 // CA trusted AND HTTPS gateway bound
    error: '',
    busy: false,
};

async function refreshRegisteredSites() {
    if (!state.user) {
        state.registeredSites = [];
        state.hostsInstalled = {};
        state.siteAnalytics = {};
        return;
    }
    try {
        const rows = await MyDomains();
        state.registeredSites = (rows || []).filter(r => r.status === 'approved');
        const names = state.registeredSites.map(r => r.name);
        state.hostsInstalled = names.length ? (await HostsEntryStatus(names)) || {} : {};
    } catch {
        state.registeredSites = [];
        state.hostsInstalled = {};
    }
    try { state.gatewayAddr    = (await GatewayAddr())    || state.gatewayAddr; } catch {}
    try { state.gatewayTLSAddr = (await GatewayTLSAddr()) || ''; } catch {}
    try { state.httpsReady     = !!(await HTTPSReady()); } catch { state.httpsReady = false; }
}

async function refreshSiteAnalytics() {
    if (!state.user || !state.registeredSites.length) {
        state.siteAnalytics = {};
        return;
    }
    try {
        const names = state.registeredSites.map(r => r.name);
        const rows = await SiteAnalyticsBatch(names);
        const m = {};
        (rows || []).forEach(r => { if (r && r.name) m[r.name] = r; });
        state.siteAnalytics = m;
    } catch {}
}

// Canonical "what URL should I show the user" picker.
//   - If HTTPS is ready (CA trusted + daemon TLS bound) AND the hosts
//     entry is installed, we serve `https://name.alt[:port]/` — green
//     padlock, no browser warning.
//   - Otherwise the http variant, port suffix dropped when the daemon
//     got the natural port (80 or 443).
//   - The fallback `http://127.0.0.1:<port>/n/name.alt/` always works
//     without DNS/hosts setup, regardless of HTTPS state.
function siteURLs(name) {
    const httpAddr = state.gatewayAddr || '127.0.0.1:9080';
    const tlsAddr  = state.gatewayTLSAddr || '';
    const httpsReady = state.httpsReady && !!tlsAddr;

    const portOf = a => {
        const m = a.match(/:(\d+)$/);
        return m ? m[1] : '';
    };
    const httpPort = portOf(httpAddr);
    const tlsPort  = portOf(tlsAddr);

    const httpSuffix = httpPort === '80' ? '' : ':' + httpPort;
    const tlsSuffix  = tlsPort  === '443' ? '' : ':' + tlsPort;

    const cleanHTTP  = 'http://'  + name + httpSuffix + '/';
    const cleanHTTPS = tlsAddr ? 'https://' + name + tlsSuffix + '/' : null;
    const clean = (httpsReady && cleanHTTPS) ? cleanHTTPS : cleanHTTP;

    return {
        clean,
        cleanHTTP,
        cleanHTTPS,
        fallback: 'http://' + httpAddr + '/n/' + name + '/',
        installed: !!state.hostsInstalled[name],
        httpsReady,
        tlsAvailable: !!tlsAddr,
    };
}
function bestSiteURL(name) {
    const u = siteURLs(name);
    return u.installed ? u.clean : u.fallback;
}

const root = () => document.getElementById('app');

function render() {
    const r = root();
    if (!r) return;
    r.innerHTML = '';

    // topbar persists across views
    const topbar = el('header', { class: 'topbar' });
    topbar.append(el('div', { class: 'brand' }, [
        el('span', { class: 'dot' }),
        text('A L T N E T'),
        el('span', { class: 'brand-suffix' }, [text('Studio')]),
    ]));
    const right = el('div', { class: 'topbar-right' });
    if (state.user) {
        right.append(el('span', { class: 'mono' }, [text(state.user.email)]));
        if (state.user.is_admin) right.append(el('span', { class: 'badge' }, [text('admin')]));
        right.append(link('Report a site', () => {
            state.reportTarget = '';
            go('report');
        }));
        right.append(link('Change password', () => {
            state.pendingResetEmail = state.user.email;
            go('reset-request');
        }));
        right.append(link('Delete account', async () => {
            if (!confirm(`Permanently delete the AltNet account ${state.user.email}? Your registered .alt names lose admin-side tracking. Any sites already published from this PC stay live (they're keyed to the daemon's identity, not your account). This can't be undone.`)) return;
            try {
                await DeleteAccount();
                state.user = null;
                state.registeredSites = [];
                state.hostsInstalled = {};
                showToast('Account deleted.');
                go('home');
            } catch (err) {
                showToast('Could not delete: ' + ((err && err.message) || err));
            }
        }));
        right.append(link('Log out', async () => {
            await Logout();
            state.user = null;
            go('home');
        }));
    } else if (state.view !== 'loading') {
        right.append(link('Sign in', () => go('login')));
    }
    // Always-visible Terms link (legal requirement to make ToS findable).
    right.append(link('Terms', () => go('terms')));
    topbar.append(right);
    r.append(topbar);

    // Strong, persistent warning when the app isn't running as root on
    // Linux — it can't bind port 80, configure DNS, or install its CA, so
    // .alt sites won't load and "Be a node" is disabled until relaunched.
    if (state.needsRoot) {
        r.append(el('div', { class: 'root-warning' }, [
            el('strong', {}, [text('⚠ Run AltNet Studio as root')]),
            el('div', {}, [text(
                'On Linux the app must run as root to open .alt sites (port 80), ' +
                'resolve .alt names, and install its certificate. Quit and relaunch with:'
            )]),
            el('code', {}, [text('sudo -E ./AltNetStudio')]),
        ]));
    }

    // main viewport
    const vp = el('main', { class: 'viewport' });
    let view;
    switch (state.view) {
        case 'loading':       view = renderLoading();          break;
        case 'home':          view = renderHome();             break;
        case 'signup':        view = renderSignup();           break;
        case 'verify':        view = renderVerify();           break;
        case 'login':         view = renderLogin();            break;
        case 'reset-request': view = renderResetRequest();     break;
        case 'reset-confirm': view = renderResetConfirm();     break;
        case 'node':          view = renderNode();             break;
        case 'hosting-terms': view = renderHostingTerms();     break;
        case 'register':      view = renderRegister();         break;
        case 'registered':    view = renderRegisteredSites(); break;
        case 'admin':         view = renderAdmin();            break;
        case 'admin-reports': view = renderAdminReports();     break;
        case 'report':        view = renderReportSite();       break;
        case 'terms':         view = renderTerms();            break;
        default:              view = renderHome();
    }
    vp.append(view);
    r.append(vp);
}

function go(view, extra = {}) {
    Object.assign(state, { view, error: '', ...extra });
    render();
    // When navigating somewhere that depends on the registered-sites
    // list, refresh in the background and re-render once it's back.
    // Cheaper than caching invalidations everywhere.
    if ((view === 'home' || view === 'registered') && state.user) {
        refreshRegisteredSites().then(() => {
            if (state.view === view) render();
        });
    }
}

function postAuth(user) {
    state.user = user;
    // Kick off a refresh — the third home card depends on this.
    refreshRegisteredSites().then(() => {
        if (state.view === 'home') render();
    });
    const dest = state.pendingAction === 'register' ? 'register'
              : state.pendingAction === 'admin'    ? 'admin'
              : 'home';
    state.pendingAction = null;
    go(dest);
}

// ── views ──

function renderLoading() {
    const v = el('div', { class: 'view' });
    const c = el('div', { class: 'card tall' });
    c.append(el('p', { class: 'faint' }, [text('Booting…')]));
    v.append(c);
    return v;
}

function renderHome() {
    const v = el('div', { class: 'view view-wide' });

    v.append(el('h1', {}, [text('A peer-to-peer alternative internet.')]));
    v.append(el('p', { class: 'tagline' }, [
        text('Run a node, or claim a '),
        el('span', { class: 'mono', style: 'color: var(--accent)' }, [text('.alt')]),
        text(' name and put a site on the network. No central server.'),
    ]));

    const choices = el('div', { class: 'choices' });
    choices.append(choiceCard(
        'Be a node',
        'One click and this PC starts carrying traffic. AltNet remembers, and your node comes back automatically when Windows boots. No account needed.',
        () => startBeingANode(),
    ));
    choices.append(choiceCard(
        'Register a .alt domain',
        state.user
            ? 'Type the name you want, get it approved, then publish your folder. It goes live on the network.'
            : 'Pick a name and request approval. You\'ll need an account first — takes 30 seconds.',
        () => {
            if (state.user) go('register');
            else { state.pendingAction = 'register'; go('signup'); }
        },
    ));
    // The third card appears only once the signed-in user has at least
    // one approved domain — there's nothing to show otherwise.
    if (state.user && state.registeredSites.length > 0) {
        const label = state.registeredSites.length === 1
            ? '1 site live on AltNet'
            : state.registeredSites.length + ' sites live on AltNet';
        choices.append(choiceCard(
            'Registered sites',
            label + '. Open, re-publish, or copy its address.',
            () => go('registered'),
        ));
    }
    v.append(choices);

    if (state.user?.is_admin) {
        const adminCard = el('div', { class: 'card' });
        adminCard.append(el('div', { class: 'section-head' }, [
            el('h3', {}, [text('Admin')]),
            el('span', { class: 'badge' }, [text('admin')]),
        ]));
        adminCard.append(el('p', {}, [text('Review and decide on pending domain requests, and triage abuse reports.')]));
        adminCard.append(el('div', { class: 'row' }, [
            button('Domain queue', () => go('admin')),
            button('Reports queue', () => go('admin-reports'), 'ghost'),
        ]));
        v.append(adminCard);
    }
    return v;
}


// startBeingANode is the click handler for the "Be a node" choice on
// home. First time only, it routes through the hosting-terms screen
// so the user explicitly accepts the legal disclaimer. After that
// flag is set in prefs, future clicks go straight to EnableNodeMode.
async function startBeingANode() {
    try {
        const accepted = await HostingTermsAccepted();
        if (!accepted) {
            go('hosting-terms');
            return;
        }
        await EnableNodeMode();
        go('node');
    } catch (err) {
        showToast('Could not start node: ' + (err?.message || err));
    }
}

// renderHostingTerms is the legal disclaimer interposed between the
// "Be a node" click and the actual EnableNodeMode call. Plain-language
// summary of what it means to run a node: you'll be holding pieces of
// other people's sites on your disk and forwarding requests. Once
// accepted, the timestamp is stamped into prefs and this screen never
// shows again unless the user wipes their config.
function renderHostingTerms() {
    const v = el('div', { class: 'view' });
    const c = el('div', { class: 'card tall' });
    c.append(el('h1', {}, [text('Before you turn this on.')]));
    c.append(el('p', { class: 'tagline' }, [text('A few things to know about running a node.')]));

    const list = el('ul', { class: 'feature-list' });
    [
        ['You will host other people\'s content.',
         'Your PC will hold and serve pieces of sites you didn\'t publish. This is how AltNet stays up without central servers.'],
        ['You won\'t see what\'s in those pieces.',
         'Content is chunked and content-addressed; your node doesn\'t know which file each chunk belongs to until someone fetches it.'],
        ['We block known-bad content.',
         'AltNet ships with a blocklist of chunk hashes (CSAM and other illegal content) that your node refuses to store or serve. New bad hashes get added as we find them.'],
        ['You can report anything.',
         'If you find a .alt site hosting something illegal, use the Report button — admins review and revoke names. Revocation propagates so other nodes drop the chunks too.'],
        ['You can stop any time.',
         'Disable Node Mode from the Node screen. Your store gets wiped and your PC stops carrying traffic.'],
    ].forEach(([title, body]) => {
        const li = el('li', {}, [
            el('strong', {}, [text(title + ' ')]),
            text(body),
        ]);
        list.append(li);
    });
    c.append(list);

    c.append(el('p', { class: 'faint' }, [
        text('This is the same legal framework as any other content host (YouTube, GitHub, etc.). By clicking accept you confirm you understand what running a node means and you take responsibility for using the report tools when you spot something wrong.'),
    ]));
    c.append(el('p', { class: 'faint' }, [
        text('Abuse / takedown contact: '),
        el('span', { class: 'mono' }, [text('abuse.report@panmox.org')]),
        text('. Full terms: '),
    ]));
    const termsLink = link('Terms of Service', () => go('terms'));
    c.append(termsLink);

    c.append(errorEl());

    const accept = button('I understand — turn it on', async () => {
        if (state.busy) return;
        await runBusy(accept, 'Starting…', async () => {
            await AcceptHostingTerms();
            await EnableNodeMode();
            go('node');
        });
    });
    const cancel = button('Cancel', () => go('home'), 'ghost');
    c.append(el('div', { class: 'row' }, [accept, cancel]));

    v.append(c);
    return v;
}

// renderTerms is the AltNet Terms of Service page, accessible from
// the always-visible "Terms" link in the topbar. Plain-language, no
// lawyer-speak. Doubles as the public takedown/abuse contact page —
// abuse.report@panmox.org is the designated agent under DMCA / DSA-style
// intermediary frameworks.
function renderTerms() {
    const v = el('div', { class: 'view view-wide' });
    const c = el('div', { class: 'card' });
    c.append(el('h1', {}, [text('Terms of Service')]));
    c.append(el('p', { class: 'tagline' }, [text('Plain English. No surprises.')]));

    const sections = [
        ['What AltNet is',
         'AltNet is a peer-to-peer network for hosting websites and files under the .alt namespace. Your AltNet Studio app is a tool that connects to that network. The network has no central server; content is stored and served by participating nodes (people\'s PCs).'],
        ['Your responsibilities as a publisher',
         'When you register a .alt name and publish content under it, you are the publisher. You are responsible for that content being legal where you live and where it can be reached. Don\'t publish illegal material — child sexual abuse material, content that infringes copyright, fraud, malware, threats. We will revoke any name found hosting such content and pursue legal options when appropriate.'],
        ['Your responsibilities as a node operator',
         'Running a node means your PC stores and serves pieces of other people\'s content. The content is chunked and content-addressed; you don\'t see what files you are serving. We ship a blocklist of known-bad content hashes (CSAM and similar) that your node refuses to store or serve. You can stop being a node at any time from the Node screen.'],
        ['How we moderate',
         'We review every .alt name request before it goes live. Any user can report any name via the Report a site link. Reports are reviewed within 72 hours. If a report is upheld, the name is revoked — taken off the network — and a signed deletion record is broadcast so cached chunks get purged from other nodes.'],
        ['Three strikes',
         'If three of your .alt names get revoked for abuse, your AltNet account is automatically suspended. You can appeal to abuse.report@panmox.org.'],
        ['Reporting abuse',
         'Found something illegal? Report it in-app via the Report a site link, or email abuse.report@panmox.org directly. Include the .alt name and what is wrong. We aim to respond within 72 hours.'],
        ['Takedown requests',
         'Copyright holders, law enforcement, or anyone with a legal claim: send a takedown notice to abuse.report@panmox.org with (1) the .alt name, (2) a description of the content, (3) the legal basis for the request, (4) your contact info. We act in good faith on every legitimate notice.'],
        ['No warranty',
         'AltNet Studio is open-source software provided as-is under the GNU GPL v3. There is no warranty. If it breaks your PC, eats your files, or gives your cat insomnia, that\'s on you. We do our best to ship reliable software but make no guarantees.'],
        ['Account deletion',
         'You can delete your account from the topbar Delete account link. Your .alt names lose registrar tracking; published content on the network may persist briefly until name records expire (about 7 days).'],
        ['Changes to these terms',
         'We may update these terms. Significant changes will be announced via panmox.org. Continued use after an update means you accept the new terms.'],
    ];
    sections.forEach(([title, body]) => {
        c.append(el('h3', {}, [text(title)]));
        c.append(el('p', {}, [text(body)]));
    });

    const contact = el('div', { class: 'card', style: 'background: rgba(0,200,255,.04); border-color: rgba(0,200,255,.18); margin-top: 24px;' });
    contact.append(el('h3', {}, [text('Contact')]));
    contact.append(el('p', { class: 'mono' }, [text('abuse.report@panmox.org')]));
    contact.append(el('p', { class: 'faint' }, [text('Takedown, legal, and abuse reports. Designated agent under DMCA / DSA-style frameworks.')]));
    c.append(contact);

    c.append(el('div', { class: 'row' }, [button('Back', () => go('home'), 'ghost')]));
    v.append(c);
    return v;
}

function renderSignup() {
    const v = el('div', { class: 'view' });
    const c = el('div', { class: 'card tall' });
    c.append(el('h1', {}, [text('Create your account')]));
    c.append(el('p', {}, [text('You only need an account to register a domain. We\'ll email a 6-digit code to confirm it\'s really you.')]));

    const email = input('email', 'Email', state.pendingEmail);
    const password = input('password', 'Password (8+ characters)');
    c.append(email.label, password.label);
    c.append(errorEl());

    const submit = button('Create account', async () => {
        if (state.busy) return;
        const e = email.input.value.trim();
        const p = password.input.value;
        if (!e || !p) { showError('email and password are required'); return; }
        await runBusy(submit, 'Creating…', async () => {
            await Signup(e, p);
            state.pendingEmail = e;
            go('verify');
        });
    });
    c.append(el('div', { class: 'row' }, [submit, button('Back', () => go('home'), 'ghost')]));
    setTimeout(() => email.input.focus(), 0);
    v.append(c);
    return v;
}

function renderVerify() {
    const v = el('div', { class: 'view' });
    const c = el('div', { class: 'card tall' });
    c.append(el('h1', {}, [text('Check your email')]));
    c.append(el('p', {}, [
        text('We sent a 6-digit code to '),
        el('strong', { class: 'mono', style: 'color: var(--text)' }, [text(state.pendingEmail || 'your email')]),
        text('.'),
    ]));

    const code = input('text', 'Verification code');
    code.input.setAttribute('inputmode', 'numeric');
    code.input.setAttribute('maxlength', '6');
    code.input.setAttribute('autocomplete', 'one-time-code');
    c.append(code.label);
    c.append(errorEl());

    const submit = button('Verify', async () => {
        if (state.busy) return;
        const v2 = code.input.value.trim();
        if (v2.length !== 6) { showError('enter the 6-digit code'); return; }
        await runBusy(submit, 'Verifying…', async () => {
            const user = await Verify(state.pendingEmail, v2);
            postAuth(user);
        });
    });
    c.append(el('div', { class: 'row' }, [submit, button('Back', () => go('signup'), 'ghost')]));
    setTimeout(() => code.input.focus(), 0);
    v.append(c);
    return v;
}

function renderLogin() {
    const v = el('div', { class: 'view' });
    const c = el('div', { class: 'card tall' });
    c.append(el('h1', {}, [text('Sign in')]));

    const email = input('email', 'Email');
    const password = input('password', 'Password');
    c.append(email.label, password.label);
    c.append(errorEl());

    const submit = button('Sign in', async () => {
        if (state.busy) return;
        const e = email.input.value.trim();
        const p = password.input.value;
        if (!e || !p) { showError('email and password are required'); return; }
        await runBusy(submit, 'Signing in…', async () => {
            const user = await Login(e, p);
            postAuth(user);
        });
    });
    c.append(el('div', { class: 'row' }, [submit, button('Back', () => go('home'), 'ghost')]));
    c.append(el('p', { class: 'faint', style: 'text-align: center; margin-top: 4px' }, [
        link('Forgot password?', () => {
            state.pendingResetEmail = email.input.value.trim();
            go('reset-request');
        }),
    ]));
    setTimeout(() => email.input.focus(), 0);
    v.append(c);
    return v;
}

function renderResetRequest() {
    const v = el('div', { class: 'view' });
    const c = el('div', { class: 'card tall' });
    c.append(el('h1', {}, [text(state.user ? 'Change password' : 'Reset password')]));
    c.append(el('p', { class: 'tagline' }, [text('We\'ll email a 6-digit code. Use it on the next screen along with your new password.')]));

    const initial = state.pendingResetEmail || state.user?.email || '';
    const email = input('email', 'Email', initial);
    c.append(email.label, errorEl());

    const submit = button('Send code', async () => {
        if (state.busy) return;
        const e = email.input.value.trim();
        if (!e) { showError('email is required'); return; }
        await runBusy(submit, 'Sending…', async () => {
            await RequestPasswordReset(e);
            state.pendingResetEmail = e;
            go('reset-confirm');
        });
    });
    const backDest = state.user ? 'home' : 'login';
    c.append(el('div', { class: 'row' }, [submit, button('Back', () => go(backDest), 'ghost')]));
    setTimeout(() => email.input.focus(), 0);
    v.append(c);
    return v;
}

function renderResetConfirm() {
    const v = el('div', { class: 'view' });
    const c = el('div', { class: 'card tall' });
    c.append(el('h1', {}, [text('Set a new password')]));
    c.append(el('p', {}, [
        text('We sent a 6-digit code to '),
        el('strong', { class: 'mono', style: 'color: var(--text)' }, [text(state.pendingResetEmail || 'your email')]),
        text('. After you change the password, you\'ll need to log in again.'),
    ]));

    const code = input('text', 'Verification code');
    code.input.setAttribute('inputmode', 'numeric');
    code.input.setAttribute('maxlength', '6');
    code.input.setAttribute('autocomplete', 'one-time-code');
    const newPw = input('password', 'New password (4+ characters)');
    c.append(code.label, newPw.label, errorEl());

    const submit = button('Change password', async () => {
        if (state.busy) return;
        const v2 = code.input.value.trim();
        const p = newPw.input.value;
        if (v2.length !== 6) { showError('enter the 6-digit code'); return; }
        if (p.length < 4) { showError('new password must be at least 4 characters'); return; }
        await runBusy(submit, 'Updating…', async () => {
            await ConfirmPasswordReset(state.pendingResetEmail, v2, p);
            state.user = null;
            state.pendingResetEmail = '';
            go('login');
        });
    });
    c.append(el('div', { class: 'row' }, [submit, button('Back', () => go('reset-request'), 'ghost')]));
    setTimeout(() => code.input.focus(), 0);
    v.append(c);
    return v;
}

// ── Node mode ──

let nodeStatusTimer = null;

function stopNodePolling() {
    if (nodeStatusTimer) { clearInterval(nodeStatusTimer); nodeStatusTimer = null; }
}

function renderNode() {
    stopNodePolling();
    const v = el('div', { class: 'view view-wide' });

    const head = el('div', { class: 'card' });
    head.append(el('div', { class: 'section-head' }, [
        el('h3', {}, [text('Node mode')]),
        el('span', { id: 'node-state', class: 'row', style: 'gap:8px' }, [
            el('span', { class: 'dot-status', id: 'node-dot' }),
            el('span', { id: 'node-state-text', class: 'mono', style: 'font-size:12px; color: var(--text-dim)' }, [text('starting…')]),
        ]),
    ]));
    head.append(el('h1', {}, [text('You\'re a node.')]));
    head.append(el('p', { class: 'tagline' }, [
        text('AltNet will launch with Windows and bring this node back automatically. Close the window and the daemon will be relaunched on your next sign-in.'),
    ]));

    const ctrl = el('div', { class: 'row' });
    ctrl.append(button('Register a .alt domain', () => {
        stopNodePolling();
        if (state.user) go('register');
        else { state.pendingAction = 'register'; go('signup'); }
    }));
    ctrl.append(button('Stop being a node', async (ev) => {
        if (!confirm('Stop being a node? AltNet will also be removed from Windows startup.')) return;
        await runBusy(ev.target, 'Stopping…', async () => {
            await DisableNodeMode();
            stopNodePolling();
            go('home');
        });
    }, 'danger'));
    ctrl.append(button('Back to home', () => { stopNodePolling(); go('home'); }, 'ghost'));
    head.append(ctrl);
    head.append(errorEl());
    v.append(head);

    // empty-network banner (hidden when peers > 0)
    const empty = el('div', { class: 'card', id: 'node-empty-banner',
        style: 'border-color: rgba(255,181,71,.40); background: rgba(255,181,71,.04); display: none' });
    v.append(empty);

    // seed peers card — bootstrap addresses to dial on every daemon
    // launch. With at least one entry, the node joins the DHT.
    v.append(seedPeersCard());

    // stats card
    const stats = el('div', { class: 'card' });
    stats.append(el('h3', {}, [text('Live stats')]));
    const grid = el('div', { class: 'stat-grid', id: 'node-stats' });
    grid.append(statTile('Connected peers', '–', 'peers'));
    grid.append(statTile('Stored entries', '–', 'entries'));
    grid.append(statTile('Storage used', '–', 'bytes'));
    grid.append(statTile('Uptime', '–', 'uptime'));
    stats.append(grid);

    const idRow = el('p', { class: 'faint', id: 'node-peer-id' }, [text('Peer ID will appear once the daemon is running.')]);
    stats.append(idRow);

    const gatewayRow = el('p', { class: 'muted', id: 'node-gateway' });
    stats.append(gatewayRow);
    v.append(stats);

    // logs
    const logCard = el('div', { class: 'card' });
    logCard.append(el('h3', {}, [text('Recent daemon output')]));
    logCard.append(el('div', { class: 'log', id: 'node-log' }, [text('—')]));
    v.append(logCard);

    refreshNode();
    nodeStatusTimer = setInterval(refreshNode, 2000);
    return v;
}

// seedPeersCard renders the "Connect to AltNet" panel: a textarea of
// host:port entries (one per line). Save persists to prefs and
// restarts the daemon so the new -bootstrap takes effect.
//
// Without at least one seed, a fresh install has nothing to dial and
// stays alone. Friends sharing the same list end up on the same DHT.
function seedPeersCard() {
    const card = el('div', { class: 'card' });
    card.append(el('h3', {}, [text('Connect to AltNet')]));
    card.append(el('p', { class: 'faint' }, [
        text('Your node auto-dials the built-in seeds on every launch — no setup needed. Add your own below if you want to also reach a private network or your own infrastructure.'),
    ]));

    // Show the baked-in defaults as a read-only, "auto-connected" list.
    // These can't be edited from the UI (they're a compile-time
    // constant in daemon_supervisor.go) — that's deliberate so a fresh
    // install always has somewhere to dial.
    const defaultBox = el('div', {
        style: 'background:var(--bg-input);border:1px solid var(--border-soft);border-radius:10px;padding:10px 12px;display:flex;flex-direction:column;gap:4px;font-family:"JetBrains Mono",monospace;font-size:12px;color:var(--text-dim)',
        id: 'default-seeds-box',
    });
    defaultBox.append(el('div', { class: 'faint', style: 'font-family:inherit;font-size:11px;text-transform:uppercase;letter-spacing:.8px;margin-bottom:2px' }, [text('Auto-connected')]));
    defaultBox.append(el('div', { class: 'faint', style: 'font-family:inherit' }, [text('loading…')]));
    card.append(defaultBox);
    (async () => {
        try {
            const defaults = (await GetDefaultSeedPeers()) || [];
            defaultBox.innerHTML = '';
            defaultBox.append(el('div', { class: 'faint', style: 'font-family:inherit;font-size:11px;text-transform:uppercase;letter-spacing:.8px;margin-bottom:2px' }, [text('Auto-connected (built-in)')]));
            if (defaults.length === 0) {
                defaultBox.append(el('div', { class: 'faint', style: 'font-family:inherit' }, [text('— none baked in —')]));
            } else {
                defaults.forEach(s => defaultBox.append(el('div', {}, [
                    el('span', { class: 'dot-status live', style: 'margin-right:8px' }),
                    text(s),
                ])));
            }
        } catch {}
    })();

    // User-added seeds (editable). Stacked on top of the defaults; the
    // daemon dials both.
    const area = el('textarea', {
        rows: '3',
        placeholder: 'extra-seed.example.org:9000\n203.0.113.5:9000',
        style: 'background:var(--bg-input);border:1px solid var(--border);border-radius:10px;padding:10px 12px;color:var(--text);font-family:"JetBrains Mono",monospace;font-size:13px;width:100%;resize:vertical;min-height:60px',
    });
    const areaLabel = el('label', {}, [text('Extra seeds (optional, one per line)'), area]);
    (async () => {
        try {
            const seeds = (await GetSeedPeers()) || [];
            area.value = seeds.join('\n');
            applyCount.textContent = seeds.length
                ? `${seeds.length} custom seed${seeds.length === 1 ? '' : 's'} added`
                : 'no custom seeds';
        } catch {}
    })();
    card.append(areaLabel);

    const errSlot = el('div', { class: 'error', style: 'min-height:16px' });
    const applyCount = el('span', { class: 'faint', style: 'margin-left:auto;align-self:center;font-size:12px' }, [text('')]);
    const apply = button('Apply (restarts the daemon)', async (ev) => {
        errSlot.textContent = '';
        const peers = area.value.split(/\r?\n/).map(s => s.trim()).filter(Boolean);
        await runBusy(ev.target, 'Restarting…', async () => {
            try {
                const cleaned = await SetSeedPeers(peers);
                area.value = (cleaned || []).join('\n');
                applyCount.textContent = (cleaned && cleaned.length)
                    ? `${cleaned.length} custom seed${cleaned.length === 1 ? '' : 's'} added`
                    : 'no custom seeds';
                showToast('Seed list saved; daemon restarting.');
            } catch (err) {
                errSlot.textContent = (err && err.message) ? err.message : String(err);
                throw err;
            }
        });
    });
    card.append(el('div', { class: 'row' }, [apply, applyCount]));
    card.append(errSlot);
    return card;
}

async function refreshNode() {
    let st;
    try { st = await GetNodeStatus(); } catch (err) { showError(String(err)); return; }
    if (!st) return;
    const dot = document.getElementById('node-dot');
    const stateText = document.getElementById('node-state-text');
    const alone = st.running && (st.connected_peers || 0) === 0;
    if (st.running) {
        dot?.classList.add('live');
        if (alone) dot?.classList.add('alone'); else dot?.classList.remove('alone');
        if (stateText) {
            stateText.textContent = alone
                ? 'RUNNING · ALONE'
                : ('ONLINE · ' + st.connected_peers + ' peer' + (st.connected_peers === 1 ? '' : 's'));
        }
    } else {
        dot?.classList.remove('live', 'alone');
        if (stateText) stateText.textContent = st.note || 'STOPPED';
    }
    setStat('peers',   formatInt(st.connected_peers));
    setStat('entries', formatInt(st.store_entries));
    setStat('bytes',   formatBytes(st.store_bytes || 0));
    setStat('uptime',  formatUptime(st.uptime_sec || 0));

    const peer = document.getElementById('node-peer-id');
    if (peer) {
        peer.textContent = st.peer_id ? ('peer id: ' + st.peer_id) : 'Peer ID will appear once the daemon is running.';
    }

    // Empty-network banner: honest about the fact that one node = nothing.
    const banner = document.getElementById('node-empty-banner');
    if (banner) {
        if (alone) {
            banner.style.display = '';
            banner.innerHTML = '';
            banner.append(el('h3', { style: 'color: var(--warn); letter-spacing: 1.2px' }, [text('Nobody else here yet')]));
            banner.append(el('p', {}, [
                text('Your daemon is up and listening on '),
                el('span', { class: 'mono', style: 'color: var(--text)' }, [text(st.peer_id ? st.short_id || st.peer_id.slice(0, 8) : '–')]),
                text(', but it has zero peers. Until another AltNet node connects (yours on another PC, a friend\'s, etc.), nothing on the network is actually reachable — including any '),
                el('span', { class: 'mono', style: 'color: var(--accent)' }, [text('.alt')]),
                text(' site you publish.'),
            ]));
            banner.append(el('p', { class: 'faint', style: 'margin-top: 8px' }, [
                text('Next step: run the daemon on a second machine and bootstrap it to this one. Bootstrap UI is coming; for now use the CLI flag '),
                el('span', { class: 'mono' }, [text('-bootstrap <ip>:9000')]),
                text(' on the other node.'),
            ]));
        } else {
            banner.style.display = 'none';
        }
    }

    const gw = document.getElementById('node-gateway');
    if (gw) {
        if (st.gateway_url && st.running) {
            gw.innerHTML = '';
            gw.append(text('local browse gateway: '));
            const a = document.createElement('a');
            a.href = st.gateway_url; a.target = '_blank';
            a.textContent = st.gateway_url;
            a.className = 'mono';
            gw.appendChild(a);
        } else {
            gw.textContent = '';
        }
    }
    const log = document.getElementById('node-log');
    if (log && Array.isArray(st.recent_logs)) {
        log.innerHTML = st.recent_logs.map(l =>
            `<div class="line">${escapeHTML(l)}</div>`
        ).join('') || '—';
        log.scrollTop = log.scrollHeight;
    }
}

function setStat(id, value) {
    const el = document.querySelector('#node-stats [data-stat="' + id + '"] .value');
    if (el) el.textContent = value;
}

function statTile(label, value, id) {
    const t = el('div', { class: 'stat', 'data-stat': id });
    t.append(el('span', { class: 'label' }, [text(label)]));
    t.append(el('span', { class: 'value' }, [text(value)]));
    return t;
}

// ── Register / publish ──

function renderRegister() {
    stopNodePolling();
    const v = el('div', { class: 'view view-wide' });

    const top = el('div', { class: 'card' });
    top.append(el('h3', {}, [text('Domain registration')]));
    top.append(el('h1', {}, [text('Claim a .alt name')]));
    top.append(el('p', { class: 'tagline' }, [text('Type the name you want. An admin reviews each request before it goes live.')]));

    const name = input('text', 'Name');
    name.input.setAttribute('placeholder', 'mysite.alt');
    name.input.setAttribute('autocomplete', 'off');
    top.append(name.label);

    // Short pitch the admin sees when reviewing the request. Max 100
    // chars so the queue stays scannable; the live counter helps the
    // user budget their sentence.
    const desc = el('textarea', {
        rows: '2', maxlength: '100',
        placeholder: 'What this site is — a sentence the admin sees when reviewing. e.g. "Personal blog about retrocomputing."',
        style: 'background:var(--bg-input);border:1px solid var(--border);border-radius:10px;padding:10px 12px;color:var(--text);font-family:inherit;font-size:14px;width:100%;resize:vertical;min-height:60px',
    });
    const descLabel = el('label', {}, [
        el('span', { class: 'row' }, [
            text('What it is'),
            el('span', { class: 'faint', id: 'desc-count', style: 'margin-left:auto' }, [text('0 / 100')]),
        ]),
        desc,
    ]);
    desc.addEventListener('input', () => {
        const c = document.getElementById('desc-count');
        if (c) c.textContent = desc.value.length + ' / 100';
    });
    top.append(descLabel);

    // Site content: the user picks the folder for their site. We publish
    // its chunks to the network now (so the content is available), and
    // send the resulting root with the request. On admin approval the
    // authority registers name -> root and the site goes live.
    let chosenPath = '';
    const folderStatus = el('span', { class: 'faint', style: 'margin-left:10px' }, [text('no folder chosen')]);
    const pickBtn = button('Choose site folder', async () => {
        try {
            const p = await PickDirectory();
            if (p) { chosenPath = p; folderStatus.textContent = p; }
        } catch (err) { showError(String((err && err.message) || err)); }
    }, 'ghost');
    top.append(el('label', {}, [
        el('span', {}, [text('Site folder')]),
        el('div', { class: 'row' }, [pickBtn, folderStatus]),
    ]));
    top.append(errorEl());

    const reqBtn = button('Submit for approval', async () => {
        const n = name.input.value.trim().toLowerCase();
        const d = desc.value.trim();
        if (!n) { showError('type a name first'); return; }
        if (!d) { showError('write a short description so the admin knows what this site is'); return; }
        if (d.length > 100) { showError('description must be 100 characters or fewer'); return; }
        if (!chosenPath) { showError('choose the folder that holds your site (e.g. with an index.html)'); return; }
        await runBusy(reqBtn, 'Publishing…', async () => {
            // 1) publish the content (chunks) and get its root hash
            const pub = await PublishSite(n, [chosenPath]);
            // 2) submit the request with that root; admin approval will
            //    register the authority-signed name -> root.
            await RequestDomain(n, d, (pub && pub.root) || '');
            name.input.value = '';
            desc.value = '';
            chosenPath = '';
            folderStatus.textContent = 'no folder chosen';
            const c = document.getElementById('desc-count');
            if (c) c.textContent = '0 / 100';
            await refreshMyDomains();
            showToast('Submitted for approval. The admin reviews it before it goes live.');
        });
    });
    top.append(el('div', { class: 'row' }, [reqBtn, button('Back', () => go('home'), 'ghost')]));
    v.append(top);

    const list = el('div', { class: 'card' });
    list.append(el('div', { class: 'section-head' }, [
        el('h3', {}, [text('Your domains')]),
        link('Refresh', () => refreshMyDomains()),
    ]));
    list.append(el('div', { class: 'domain-list', id: 'my-domains' }, [
        el('p', { class: 'faint' }, [text('Loading…')]),
    ]));
    v.append(list);

    refreshMyDomains();
    return v;
}

async function refreshMyDomains() {
    const host = document.getElementById('my-domains');
    if (!host) return;
    try {
        const rows = await MyDomains();
        host.innerHTML = '';
        if (!rows || rows.length === 0) {
            host.append(el('p', { class: 'faint' }, [text('No domains yet. Request one above.')]));
            return;
        }
        rows.forEach(r => host.append(domainRowEl(r, 'mine')));
    } catch (err) {
        host.innerHTML = '';
        host.append(el('p', { class: 'error' }, [text(String(err))]));
    }
}

function domainRowEl(row, mode) {
    const r = el('div', { class: 'domain-row' });
    const leftKids = [
        el('div', { class: 'name' }, [text(row.name)]),
        el('div', { class: 'meta' }, [text(formatTime(row.created_at) + (row.user_email ? ' · ' + row.user_email : ''))]),
    ];
    if (row.description) {
        leftKids.push(el('div', { class: 'meta', style: 'color: var(--text-dim); margin-top:4px' }, [text('"' + row.description + '"')]));
    }
    const left = el('div', {}, leftKids);
    r.append(left);
    const right = el('div', { class: 'actions' });
    right.append(el('span', { class: 'badge ' + row.status }, [text(row.status)]));
    if (mode === 'mine' && row.status === 'approved') {
        right.append(button('Publish site', () => publishFlow(row), ''));
    }
    if (mode === 'admin') {
        right.append(button('Approve', async () => {
            await AdminDecide(row.id, 'approve');
            await refreshAdminPending();
        }, ''));
        right.append(button('Decline', async () => {
            await AdminDecide(row.id, 'decline');
            await refreshAdminPending();
        }, 'danger'));
    }
    r.append(right);
    return r;
}

// publishFlow opens a modal that lets the user build a list of folders
// and files, then runs the publish *inside* the modal so a long
// operation has visible feedback and failures stay in context.
//
// Layout: card is a vertical flex with a scrollable middle (the item
// list) and a sticky action row at the bottom, so adding 50 files
// doesn't push Publish off-screen.
function publishFlow(row) {
    showError('');
    const items = []; // [{path, isDir, label}]

    const overlay = document.createElement('div');
    overlay.style.cssText = 'position:fixed;inset:0;background:rgba(0,0,0,.72);backdrop-filter:blur(10px);-webkit-backdrop-filter:blur(10px);z-index:100;display:flex;align-items:center;justify-content:center;animation:fade-in-up .25s ease both;padding:24px';

    const card = el('div', { class: 'card', style: 'max-width:580px;width:100%;max-height:86vh;display:flex;flex-direction:column;gap:14px' });
    card.append(el('h2', {}, [text('Publish ' + row.name)]));
    card.append(el('p', { class: 'faint' }, [
        text('Add any combination of folders and files. Folders keep their name as a subdirectory; files go to the site root.'),
    ]));

    // Scrollable item list. flex: 1 so it takes the available middle
    // space; max-height caps it so the buttons stay visible even on
    // small windows.
    const listHost = el('div', { class: 'domain-list',
        style: 'flex:1;min-height:80px;max-height:46vh;overflow-y:auto;padding-right:4px' });
    const countLabel = el('p', { class: 'faint', style: 'margin:0' });
    function refresh() {
        listHost.innerHTML = '';
        if (items.length === 0) {
            listHost.append(el('p', { class: 'faint', style: 'text-align:center;padding:18px 0' }, [text('Nothing added yet.')]));
        } else {
            items.forEach((it, i) => {
                const r = el('div', { class: 'domain-row' });
                const left = el('div', { style: 'min-width:0;flex:1' }, [
                    el('div', { class: 'name' }, [text((it.isDir ? '/ ' : '· ') + it.label)]),
                    el('div', { class: 'meta', style: 'word-break:break-all' }, [text(it.path)]),
                ]);
                r.append(left);
                const rm = el('button', { class: 'tiny ghost' }, [text('Remove')]);
                rm.addEventListener('click', () => { items.splice(i, 1); refresh(); });
                r.append(rm);
                listHost.append(r);
            });
        }
        const folderCount = items.filter(i => i.isDir).length;
        const fileCount = items.length - folderCount;
        countLabel.textContent = items.length === 0
            ? ''
            : `${items.length} item${items.length === 1 ? '' : 's'} — ${folderCount} folder${folderCount === 1 ? '' : 's'}, ${fileCount} file${fileCount === 1 ? '' : 's'}`;
    }
    refresh();
    card.append(listHost);
    card.append(countLabel);

    // Add-row + actions live outside the scroll area so they're always
    // reachable, no matter how big the list gets.
    const addRow = el('div', { class: 'row' });
    const addFolder = el('button', { class: 'ghost' }, [text('+ Folder')]);
    addFolder.addEventListener('click', async () => {
        try {
            const dir = await PickDirectory();
            if (dir) { items.push({ path: dir, isDir: true, label: filenameOf(dir) }); refresh(); }
        } catch {}
    });
    const addFiles = el('button', { class: 'ghost' }, [text('+ Files')]);
    addFiles.addEventListener('click', async () => {
        try {
            const files = await PickFiles();
            if (files) {
                files.forEach(f => items.push({ path: f, isDir: false, label: filenameOf(f) }));
                refresh();
            }
        } catch {}
    });
    addRow.append(addFolder, addFiles);
    card.append(addRow);

    // Inline error slot — failures show up here, in the user's eye,
    // instead of being hidden behind a closed modal.
    const errSlot = el('div', { class: 'error', style: 'min-height:18px' });
    card.append(errSlot);

    const actions = el('div', { class: 'row' });
    const publishBtn = el('button', {}, [text('Publish')]);
    const cancelBtn = el('button', { class: 'ghost' }, [text('Cancel')]);
    publishBtn.addEventListener('click', async () => {
        if (items.length === 0) { errSlot.textContent = 'add at least one folder or file first'; return; }
        errSlot.textContent = '';
        const oldHTML = publishBtn.innerHTML;
        publishBtn.disabled = true;
        cancelBtn.disabled = true;
        publishBtn.innerHTML = '';
        publishBtn.append(el('span', { class: 'spinner' }), text(' Publishing…'));
        try {
            const res = await PublishToOwnedDomain(row.name, items.map(it => it.path));
            cleanup();
            showPublishedBanner(res);
            // Make sure the home card reflects this even on first publish.
            refreshRegisteredSites();
        } catch (err) {
            errSlot.textContent = (err && err.message) ? err.message : String(err);
            publishBtn.disabled = false;
            cancelBtn.disabled = false;
            publishBtn.innerHTML = oldHTML;
        }
    });
    cancelBtn.addEventListener('click', () => {
        if (publishBtn.disabled) return; // mid-publish, don't bail
        cleanup();
    });
    actions.append(publishBtn, cancelBtn);
    card.append(actions);

    overlay.appendChild(card);
    document.body.appendChild(overlay);

    const onKey = (e) => {
        if (e.key === 'Escape' && !publishBtn.disabled) cleanup();
    };
    document.addEventListener('keydown', onKey);
    function cleanup() {
        overlay.remove();
        document.removeEventListener('keydown', onKey);
    }
}

function showPublishedBanner(res) {
    const host = document.getElementById('my-domains');
    if (!host) return;
    const banner = el('div', { class: 'card', style: 'border-color: rgba(40,200,120,.4)' });
    banner.append(el('h3', { style: 'color: var(--ok)' }, [text('Published')]));
    banner.append(el('p', {}, [
        el('span', { class: 'mono', style: 'color: var(--text)' }, [text(res.name)]),
        text(' is now live with ' + res.entry_count + ' file(s).'),
    ]));

    const installed = !!state.hostsInstalled[res.name];
    const browseURL = installed
        ? 'http://' + res.name + ':9080/'
        : 'http://' + res.gateway_url + '/n/' + res.name + '/';
    const a = document.createElement('a');
    a.href = browseURL; a.target = '_blank';
    a.className = 'mono';
    a.textContent = browseURL + '  →  open ' + res.name;
    banner.append(a);

    if (!installed) {
        // Offer the one-click upgrade right here so the user doesn't
        // have to find the Registered Sites tab to discover it.
        const setup = button('Set up clean URL for ' + res.name, async (ev) => {
            await runBusy(ev.target, 'Installing…', async () => {
                try {
                    await InstallHostsEntry(res.name);
                } catch (err) {
                    showToast('Hosts file update was blocked: ' + ((err && err.message) || err));
                    throw err;
                }
                await refreshRegisteredSites();
                // Update this banner in place.
                a.href = 'http://' + res.name + ':9080/';
                a.textContent = 'http://' + res.name + ':9080/  →  open ' + res.name;
                setup.remove();
                hint.remove();
            });
        });
        banner.append(setup);
        const hint = el('p', { class: 'faint' }, [
            text('Adds '),
            el('span', { class: 'mono' }, [text('127.0.0.1 ' + res.name)]),
            text(' to your Windows hosts file (one-time UAC prompt). After that, '),
            el('span', { class: 'mono' }, [text('http://' + res.name + ':9080')]),
            text(' works in any browser on this PC.'),
        ]);
        banner.append(hint);
    }

    banner.append(el('p', { class: 'faint' }, [text('root: ' + res.root)]));
    host.parentNode.insertBefore(banner, host);
}

function filenameOf(p) {
    const m = p.match(/[^\\\/]+$/);
    return m ? m[0] : p;
}

// ── Registered sites (Cloudflare-style dashboard) ──

let analyticsTimer = null;
function stopAnalyticsPolling() {
    if (analyticsTimer) { clearInterval(analyticsTimer); analyticsTimer = null; }
}

function renderRegisteredSites() {
    stopNodePolling();
    stopAnalyticsPolling();
    const v = el('div', { class: 'view view-wide' });

    const head = el('div', { class: 'card' });
    head.append(el('div', { class: 'section-head' }, [
        el('h3', {}, [text('Sites dashboard')]),
        link('Refresh', async () => {
            await refreshRegisteredSites();
            await refreshSiteAnalytics();
            render();
        }),
    ]));
    head.append(el('h1', {}, [text('Registered sites')]));
    head.append(el('p', { class: 'tagline' }, [
        text('Live request counts, bytes served, unique visitors per site. Re-publish to swap content, or take a site down to stop serving it from this node.'),
    ]));
    head.append(el('div', { class: 'row' }, [
        button('Back to home', () => go('home'), 'ghost'),
    ]));
    v.append(head);

    // HTTPS banner — installs the per-install root CA into the Windows
    // trust store. Replaces `http://name.alt/` with `https://name.alt/`
    // (green padlock) on every site card once it's done. Hidden once
    // it's already on.
    v.append(httpsBanner());

    if (!state.registeredSites || state.registeredSites.length === 0) {
        const empty = el('div', { class: 'card' });
        empty.append(el('p', { class: 'faint' }, [
            text('No live sites yet. Register a domain and publish to it; the site will appear here.'),
        ]));
        v.append(empty);
        return v;
    }

    const grid = el('div', { class: 'site-grid' });
    state.registeredSites.forEach(row => grid.append(siteCard(row)));
    v.append(grid);

    // Live numbers: fetch once now, then on a 3s interval. Both paths
    // patch the existing cards in-place via updateSiteCardStats — we
    // MUST NOT call render() from here, because renderRegisteredSites
    // is what's setting up this poll. Re-rendering re-enters this
    // function and fires another fetch… infinite loop that pegs CPU
    // and (because `.view` restarts its fade-in-up animation every
    // render) leaves the page stuck at opacity 0.
    refreshSiteAnalytics().then(() => {
        if (state.view === 'registered') updateSiteCardStats();
    });
    analyticsTimer = setInterval(async () => {
        if (state.view !== 'registered') { stopAnalyticsPolling(); return; }
        await refreshSiteAnalytics();
        updateSiteCardStats();
    }, 3000);

    return v;
}

// updateSiteCardStats patches in fresh numbers without redrawing the
// whole grid (which would lose the in-flight button-disabled states).
function updateSiteCardStats() {
    state.registeredSites.forEach(row => {
        const stats = state.siteAnalytics[row.name] || {};
        const root = document.querySelector(`[data-site-card="${row.name}"]`);
        if (!root) return;
        const set = (k, v) => {
            const e = root.querySelector(`[data-stat="${k}"] .value`);
            if (e) e.textContent = v;
        };
        set('requests', formatInt(stats.requests || 0));
        set('bytes',    formatBytes(stats.bytes || 0));
        set('ips',      formatInt(stats.unique_ips || 0));
        set('last',     stats.last_seen_unix ? relativeTime(stats.last_seen_unix) : 'never');
    });
}

// httpsBanner renders the "Trust the AltNet CA" toggle. Shape depends
// on state: gentle prompt before install, quiet confirmation after.
function httpsBanner() {
    const tlsAvailable = !!state.gatewayTLSAddr;
    const ready = state.httpsReady;

    const card = el('div', { class: 'card', style: ready
        ? 'border-color: rgba(40,200,120,.35); background: rgba(40,200,120,.04)'
        : 'border-color: rgba(255,181,71,.35); background: rgba(255,181,71,.04)' });

    if (!tlsAvailable) {
        card.append(el('h3', { style: 'color: var(--warn)' }, [text('HTTPS not available')]));
        card.append(el('p', { class: 'faint' }, [
            text('The daemon\'s HTTPS gateway hasn\'t come up. Restart the app and check the node screen if this persists.'),
        ]));
        return card;
    }

    if (ready) {
        card.append(el('h3', { style: 'color: var(--ok)' }, [text('HTTPS is on')]));
        card.append(el('p', { class: 'faint' }, [
            text('Your local CA is trusted; every '),
            el('span', { class: 'mono', style: 'color: var(--text)' }, [text('.alt')]),
            text(' site below opens at '),
            el('span', { class: 'mono', style: 'color: var(--ok)' }, [text('https://')]),
            text(' with a green padlock. Visitors on other machines see warnings until they install the CA too (or until we ship the browser extension).'),
        ]));
        card.append(el('div', { class: 'row' }, [
            button('Remove trust', async (ev) => {
                if (!confirm('Remove the AltNet CA from your Windows trust store? https:// links will start showing browser warnings again.')) return;
                await runBusy(ev.target, 'Removing…', async () => {
                    await DisableHTTPS();
                    await refreshRegisteredSites();
                    render();
                });
            }, 'ghost'),
        ]));
        return card;
    }

    card.append(el('h3', { style: 'color: var(--warn)' }, [text('Turn on HTTPS')]));
    card.append(el('p', {}, [
        text('Install the AltNet local CA into your Windows trust store so '),
        el('span', { class: 'mono', style: 'color: var(--text)' }, [text('https://name.alt/')]),
        text(' loads without a browser warning. It\'s safe: the CA was generated just for this PC, the private key never leaves your machine, and it\'s scoped to '),
        el('span', { class: 'mono' }, [text('.alt')]),
        text(' only — Windows will not honor it for any other domain.'),
    ]));
    card.append(el('div', { class: 'row' }, [
        button('Trust the AltNet CA', async (ev) => {
            await runBusy(ev.target, 'Installing…', async () => {
                try { await EnableHTTPS(); }
                catch (err) {
                    showToast('Could not install CA: ' + ((err && err.message) || err));
                    throw err;
                }
                await refreshRegisteredSites();
                render();
                showToast('HTTPS is on. https://*.alt now loads cleanly.');
            });
        }),
    ]));
    return card;
}

function siteCard(row) {
    const urls = siteURLs(row.name);
    const primary = urls.installed ? urls.clean : urls.fallback;
    const stats = state.siteAnalytics[row.name] || {};

    const c = el('div', { class: 'site-card', 'data-site-card': row.name });

    // Header: name + status dot + clean URL chip
    const head = el('div', { class: 'site-head' });
    head.append(el('div', { class: 'site-name' }, [
        el('span', { class: 'dot-status live' }),
        el('span', { class: 'mono' }, [text(row.name)]),
        urls.installed
            ? el('span', { class: 'badge', style: 'background:rgba(40,200,120,.12);color:var(--ok);border-color:rgba(40,200,120,.25)' }, [text('clean URL on')])
            : el('span', { class: 'badge', style: 'background:rgba(255,181,71,.10);color:var(--warn);border-color:rgba(255,181,71,.25)' }, [text('path URL only')]),
    ]));
    c.append(head);

    const urlRow = el('a', {
        href: '#',
        class: 'mono site-url',
        title: 'Open ' + primary,
    }, [text(primary)]);
    urlRow.addEventListener('click', async (e) => {
        e.preventDefault();
        try { await OpenURL(primary); }
        catch (err) { showToast('Could not open: ' + ((err && err.message) || err)); }
    });
    c.append(urlRow);

    // Stat tiles
    const stat = el('div', { class: 'stat-grid' });
    stat.append(statTileLabeled('Requests', formatInt(stats.requests || 0), 'requests'));
    stat.append(statTileLabeled('Bytes served', formatBytes(stats.bytes || 0), 'bytes'));
    stat.append(statTileLabeled('Unique visitors', formatInt(stats.unique_ips || 0), 'ips'));
    stat.append(statTileLabeled('Last hit', stats.last_seen_unix ? relativeTime(stats.last_seen_unix) : 'never', 'last'));
    c.append(stat);

    // Actions
    const actions = el('div', { class: 'row site-actions' });
    actions.append(button('Open', async () => {
        try { await OpenURL(primary); }
        catch (err) { showToast('Could not open: ' + ((err && err.message) || err)); }
    }, 'ghost'));
    actions.append(button('Re-publish', () => publishFlow(row)));
    if (!urls.installed) {
        actions.append(button('Set up clean URL', async (ev) => {
            await runBusy(ev.target, 'Installing…', async () => {
                try { await InstallHostsEntry(row.name); }
                catch (err) {
                    showToast('Hosts file update was blocked: ' + ((err && err.message) || err));
                    throw err;
                }
                await refreshRegisteredSites();
                render();
                showToast(row.name + ' now works in any browser on this PC.');
            });
        }, 'ghost'));
    } else {
        actions.append(button('Remove clean URL', async (ev) => {
            if (!confirm(`Remove the hosts-file entry for ${row.name}?`)) return;
            await runBusy(ev.target, 'Removing…', async () => {
                await RemoveHostsEntry(row.name);
                await refreshRegisteredSites();
                render();
            });
        }, 'ghost'));
    }
    actions.append(button('Take down', async (ev) => {
        if (!confirm(`Take down ${row.name}? This removes it from the network — a signed deletion purges its content from nodes and frees the name. You'd have to request it again to bring it back.`)) return;
        await runBusy(ev.target, 'Taking down…', async () => {
            try { await TakeDownSite(row.name); }
            catch (err) {
                showToast('Take-down failed: ' + ((err && err.message) || err));
                throw err;
            }
            // Also strip the local hosts entry on the way out so the
            // dashboard doesn't keep advertising a dead URL.
            if (urls.installed) {
                try { await RemoveHostsEntry(row.name); } catch {}
            }
            await refreshRegisteredSites();
            render();
            showToast(row.name + ' is down on this node.');
        });
    }, 'danger'));
    c.append(actions);

    return c;
}

// statTileLabeled is the per-site stat tile. Reuses the existing
// .stat-grid / .stat styles from the node screen.
function statTileLabeled(label, value, id) {
    const t = el('div', { class: 'stat', 'data-stat': id });
    t.append(el('span', { class: 'label' }, [text(label)]));
    t.append(el('span', { class: 'value' }, [text(value)]));
    return t;
}

function relativeTime(unix) {
    if (!unix) return 'never';
    const diff = Math.max(0, Math.floor(Date.now() / 1000) - unix);
    if (diff < 5) return 'just now';
    if (diff < 60) return diff + 's ago';
    if (diff < 3600) return Math.floor(diff / 60) + 'm ago';
    if (diff < 86400) return Math.floor(diff / 3600) + 'h ago';
    return Math.floor(diff / 86400) + 'd ago';
}

// ── Admin ──

function renderAdmin() {
    stopNodePolling();
    const v = el('div', { class: 'view view-wide' });
    const c = el('div', { class: 'card' });
    c.append(el('div', { class: 'section-head' }, [
        el('h3', {}, [text('Admin · pending requests')]),
        link('Refresh', () => refreshAdminPending()),
    ]));
    c.append(el('h1', {}, [text('Pending requests')]));
    c.append(el('p', { class: 'tagline' }, [text('Approve to put the name on the network. Decline to reject and free the slot for re-request.')]));
    c.append(errorEl());
    c.append(el('div', { class: 'domain-list', id: 'admin-list' }, [
        el('p', { class: 'faint' }, [text('Loading…')]),
    ]));

    // Take down a site: type the .alt name, confirm, revoke network-wide.
    c.append(el('div', { class: 'divider' }));
    c.append(el('h3', {}, [text('Take down a site')]));
    c.append(el('p', { class: 'tagline' }, [text('Enter a .alt name to remove it from the network (signed takedown, purges content from nodes).')]));
    const tdInput = el('input', { type: 'text', placeholder: 'badsite.alt', id: 'takedown-name', class: 'input' });
    const tdStatus = el('span', { class: 'faint', id: 'takedown-status' }, []);
    const tdBtn = button('Take down', async () => {
        const nm = (tdInput.value || '').trim().toLowerCase();
        if (!nm) { tdStatus.textContent = 'enter a name'; return; }
        if (!confirm(`Take down "${nm}" network-wide? This broadcasts a signed deletion that purges its content from nodes.`)) return;
        tdStatus.textContent = 'taking down…';
        try {
            await AdminTakedown(nm);
            tdStatus.textContent = '';
            tdInput.value = '';
            showToast(`${nm} taken down.`);
        } catch (err) {
            tdStatus.textContent = '';
            showToast('Takedown failed: ' + ((err && err.message) || err));
        }
    }, 'danger');
    c.append(el('div', { class: 'row' }, [tdInput, tdBtn, tdStatus]));

    c.append(el('div', { class: 'divider' }));
    c.append(button('Back', () => go('home'), 'ghost'));
    v.append(c);
    refreshAdminPending();
    return v;
}

async function refreshAdminPending() {
    const host = document.getElementById('admin-list');
    if (!host) return;
    try {
        const rows = await AdminPending();
        host.innerHTML = '';
        if (!rows || rows.length === 0) {
            host.append(el('p', { class: 'faint' }, [text('No pending requests.')]));
            return;
        }
        rows.forEach(r => host.append(domainRowEl(r, 'admin')));
    } catch (err) {
        host.innerHTML = '';
        host.append(el('p', { class: 'error' }, [text(String(err))]));
    }
}

// ── Reports ──

// renderReportSite is the user-facing "report this .alt name" form.
// Reached from the Report button on the registered-sites view (and
// eventually anywhere we display a .alt name). state.reportTarget
// holds the name being reported, set by the caller before go('report').
function renderReportSite() {
    const v = el('div', { class: 'view' });
    const c = el('div', { class: 'card tall' });
    c.append(el('h1', {}, [text('Report a site')]));
    const prefilled = state.reportTarget || '';
    c.append(el('p', { class: 'tagline' }, [
        text('Flag a .alt site as hosting illegal or abusive content. An admin reviews every report and can revoke the name across the network. Urgent cases can also email '),
        el('span', { class: 'mono' }, [text('abuse.report@panmox.org')]),
        text(' directly.'),
    ]));

    const nameInput = input('text', '.alt name (e.g. badsite.alt)', prefilled);
    if (prefilled) {
        nameInput.input.setAttribute('readonly', 'true');
    }
    const reasonEl = el('textarea', {
        rows: '4',
        maxlength: '500',
        placeholder: 'What\'s wrong with this site? Be specific — admins use this to decide.',
    });
    const reasonLabel = el('label', {}, [text('Reason (max 500 chars)'), reasonEl]);

    c.append(nameInput.label, reasonLabel);
    c.append(errorEl());

    const submit = button('Submit report', async () => {
        if (state.busy) return;
        const name = nameInput.input.value.trim().toLowerCase();
        const reason = reasonEl.value.trim();
        if (!name) { showError('which site are you reporting?'); return; }
        if (!reason) { showError('say what\'s wrong with the site'); return; }
        await runBusy(submit, 'Submitting…', async () => {
            await ReportSite(name, reason);
            showToast('Report submitted — admins will review.');
            state.reportTarget = null;
            go('home');
        });
    });
    const cancel = button('Cancel', () => { state.reportTarget = null; go('home'); }, 'ghost');
    c.append(el('div', { class: 'row' }, [submit, cancel]));

    v.append(c);
    return v;
}

// renderAdminReports is the admin-only abuse-report queue. Each row
// shows the reported name, the reason, the reporter's email, and two
// actions: Revoke (which marks the report revoked in the backend and
// — once #4 lands — broadcasts a signed dht_revoke), or Dismiss.
function renderAdminReports() {
    stopNodePolling();
    const v = el('div', { class: 'view view-wide' });
    const c = el('div', { class: 'card' });
    c.append(el('div', { class: 'section-head' }, [
        el('h3', {}, [text('Admin · abuse reports')]),
        link('Refresh', () => refreshAdminReports()),
    ]));
    c.append(el('h1', {}, [text('Abuse reports')]));
    c.append(el('p', { class: 'tagline' }, [text('Revoke takes the name off the network and broadcasts a deletion record so cached chunks get purged. Dismiss closes the report without action.')]));
    c.append(errorEl());
    c.append(el('div', { class: 'domain-list', id: 'admin-reports-list' }, [
        el('p', { class: 'faint' }, [text('Loading…')]),
    ]));
    c.append(button('Back', () => go('home'), 'ghost'));
    v.append(c);
    refreshAdminReports();
    return v;
}

async function refreshAdminReports() {
    const host = document.getElementById('admin-reports-list');
    if (!host) return;
    try {
        const rows = await AdminReports();
        host.innerHTML = '';
        if (!rows || rows.length === 0) {
            host.append(el('p', { class: 'faint' }, [text('No pending reports.')]));
            return;
        }
        rows.forEach(r => host.append(reportRowEl(r)));
    } catch (err) {
        host.innerHTML = '';
        host.append(el('p', { class: 'error' }, [text('Could not load reports: ' + (err?.message || err))]));
    }
}

function reportRowEl(r) {
    const row = el('div', { class: 'domain-row' });
    row.append(el('div', { class: 'domain-name mono' }, [text(r.name)]));
    row.append(el('div', { class: 'domain-meta' }, [
        text('reported by ' + (r.reporter_email || 'anonymous')),
        el('br'),
        el('span', { class: 'faint' }, [text(new Date(r.created_at * 1000).toLocaleString())]),
    ]));
    row.append(el('div', { class: 'domain-desc' }, [text(r.reason)]));
    const actions = el('div', { class: 'row' });
    actions.append(button('Revoke', async () => {
        if (!confirm(`Revoke ${r.name}? This takes it off the network and broadcasts a deletion record so other nodes purge cached chunks. The name owner cannot reclaim it. Three revocations against the same owner auto-suspends their account.`)) return;
        await AdminDecideReport(r.id, 'revoke', '', r.name);
        showToast('Revoked and broadcast. Owner counter incremented.');
        await refreshAdminReports();
    }, 'danger'));
    actions.append(button('Dismiss', async () => {
        await AdminDecideReport(r.id, 'dismiss', '', r.name);
        showToast('Dismissed.');
        await refreshAdminReports();
    }, 'ghost'));
    row.append(actions);
    return row;
}

// ── helpers ──

function el(tag, attrs = {}, children = []) {
    const node = document.createElement(tag);
    for (const [k, v] of Object.entries(attrs)) node.setAttribute(k, v);
    for (const child of children) node.appendChild(child);
    return node;
}
function text(s) { return document.createTextNode(s); }

function input(type, labelText, initial = '') {
    const inp = el('input', { type });
    if (initial) inp.value = initial;
    const lab = el('label', {}, [text(labelText), inp]);
    return { input: inp, label: lab };
}
function button(label, onClick, variant = '') {
    const b = el('button', variant ? { class: variant } : {}, [text(label)]);
    b.addEventListener('click', onClick);
    return b;
}
function link(label, onClick) {
    const a = el('a', { href: '#' }, [text(label)]);
    a.addEventListener('click', (e) => { e.preventDefault(); onClick(); });
    return a;
}
function choiceCard(title, body, onClick) {
    const c = el('div', { class: 'choice' }, [
        el('h2', {}, [text(title)]),
        el('p', {}, [text(body)]),
        el('span', { class: 'arrow' }, [text('→')]),
    ]);
    c.addEventListener('click', onClick);
    return c;
}
function errorEl() {
    const e = el('div', { class: 'error', id: 'error-slot' });
    if (state.error) e.textContent = state.error;
    return e;
}
function showError(msg) {
    state.error = msg || '';
    const slot = document.getElementById('error-slot');
    if (slot) slot.textContent = state.error;
}
async function runBusy(btn, busyText, fn) {
    state.busy = true;
    const oldHTML = btn.innerHTML;
    btn.disabled = true;
    btn.innerHTML = '';
    btn.append(el('span', { class: 'spinner' }), text(' ' + busyText));
    try { await fn(); } catch (err) {
        showError((err && err.message) ? err.message : String(err));
    } finally {
        state.busy = false;
        btn.disabled = false;
        btn.innerHTML = oldHTML;
    }
}

function formatInt(n) {
    if (n === null || n === undefined) return '–';
    return Number(n).toLocaleString();
}
function formatBytes(n) {
    if (!n) return '0 B';
    const u = ['B', 'KB', 'MB', 'GB', 'TB'];
    let i = 0, x = n;
    while (x >= 1024 && i < u.length - 1) { x /= 1024; i++; }
    return x.toFixed(x >= 100 || i === 0 ? 0 : 1) + ' ' + u[i];
}
function formatUptime(sec) {
    if (!sec) return '0s';
    const d = Math.floor(sec / 86400);
    const h = Math.floor((sec % 86400) / 3600);
    const m = Math.floor((sec % 3600) / 60);
    const s = sec % 60;
    if (d) return `${d}d ${h}h`;
    if (h) return `${h}h ${m}m`;
    if (m) return `${m}m ${s}s`;
    return `${s}s`;
}
function formatTime(ts) {
    if (!ts) return '';
    const d = new Date(ts * 1000);
    return d.toLocaleString();
}
function escapeHTML(s) {
    return String(s)
        .replace(/&/g, '&amp;')
        .replace(/</g, '&lt;')
        .replace(/>/g, '&gt;');
}

// ── toasts ──

function showToast(msg) {
    let t = document.getElementById('toast-host');
    if (!t) {
        t = el('div', { id: 'toast-host', style: 'position:fixed;top:74px;right:24px;display:flex;flex-direction:column;gap:8px;z-index:50' });
        document.body.appendChild(t);
    }
    const card = el('div', { class: 'card', style: 'padding:12px 16px;border-color:rgba(255,107,107,.45);max-width:340px;animation:fade-in-up .25s ease both' }, [
        el('span', { style: 'font-size:13px;color:var(--text)' }, [text(msg)]),
    ]);
    t.appendChild(card);
    setTimeout(() => card.remove(), 5000);
}

// ── bootstrap ──

(async function init() {
    try { state.needsRoot = await NeedsRoot(); } catch {}
    render();
    try {
        const user = await CurrentUser();
        if (user && user.email) state.user = user;
    } catch {}
    if (state.user) {
        await refreshRegisteredSites();
    }
    // Always land on the front page. The daemon still auto-starts in the
    // background if node mode was enabled (handled in Go startup) — this
    // only controls which screen the window opens on.
    go('home');
    try { console.log('backend:', await BackendURL()); } catch {}
})();
