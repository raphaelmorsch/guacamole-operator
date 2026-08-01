import * as React from 'react';
import { consoleFetchJSON } from '@openshift-console/dynamic-plugin-sdk';
import {
  Alert,
  AlertVariant,
  Bullseye,
  Button,
  Checkbox,
  EmptyState,
  EmptyStateBody,
  Form,
  FormGroup,
  PageSection,
  Spinner,
  TextInput,
  Title,
} from '@patternfly/react-core';

type PortalConfig = {
  displayName: string;
  sessionNamespace: string;
  poolName: string;
  poolNamespace: string;
  pluginName: string;
};

type KeycloakUser = {
  id: string;
  username: string;
  email?: string;
  firstName?: string;
  lastName?: string;
  enabled: boolean;
};

type SessionItem = {
  metadata?: { name?: string; namespace?: string; creationTimestamp?: string };
  spec?: {
    requester?: { subject?: string };
    poolRef?: { name?: string };
    priority?: number;
  };
  status?: {
    phase?: string;
    desktopName?: string;
    connectionName?: string;
    queuePosition?: number;
    queueLength?: number;
    message?: string;
  };
};

type BatchResultItem = {
  subject?: string;
  name?: string;
  status: 'created' | 'exists' | 'deleted' | 'error' | string;
  error?: string;
};

type BatchResponse = {
  results: BatchResultItem[];
  created?: number;
  exists?: number;
  deleted?: number;
  errors: number;
};

type PoolStatus = {
  name: string;
  namespace: string;
  phase?: string;
  desired: number;
  available: number;
  allocated: number;
  stopped: number;
  provisioning: number;
  failed: number;
  replicas: number;
  minReady: number;
  recyclePolicy: string;
  createConnections: boolean;
  powerManagement: {
    enabled: boolean;
    idleSeconds: number;
  };
  desktops?: { name: string; state: string; message?: string }[];
};

type PoolFormState = {
  replicas: string;
  minReady: string;
  recyclePolicy: string;
  createConnections: boolean;
  powerEnabled: boolean;
  idleSeconds: string;
};

type GuacamoleStatus = {
  name: string;
  namespace: string;
  phase?: string;
  routeURL?: string;
  replicas: number;
  guacdReplicas: number;
  logLevel: string;
  routeEnabled: boolean;
  openID: {
    configured: boolean;
    enabled: boolean;
    issuer?: string;
    clientID?: string;
    usernameClaimType?: string;
    scope?: string;
    extensionPriority?: string;
    redirectURI?: string;
  };
};

type GuacamoleFormState = {
  replicas: string;
  guacdReplicas: string;
  logLevel: string;
  routeEnabled: boolean;
  openIDEnabled: boolean;
  usernameClaimType: string;
  scope: string;
  extensionPriority: string;
};

const proxyBase = (pluginName: string) => `/api/proxy/plugin/${pluginName}/portal-api`;

const formatIdle = (seconds: number) => {
  if (seconds < 60) return `${seconds}s`;
  const m = Math.floor(seconds / 60);
  const s = seconds % 60;
  return s ? `${m}m ${s}s` : `${m}m`;
};

const DesktopSessionsPage: React.FC = () => {
  const [config, setConfig] = React.useState<PortalConfig | null>(null);
  const [search, setSearch] = React.useState('');
  const [users, setUsers] = React.useState<KeycloakUser[]>([]);
  const [selected, setSelected] = React.useState<Record<string, boolean>>({});
  const [selectedSessions, setSelectedSessions] = React.useState<Record<string, boolean>>({});
  const [sessions, setSessions] = React.useState<SessionItem[]>([]);
  const [pool, setPool] = React.useState<PoolStatus | null>(null);
  const [poolForm, setPoolForm] = React.useState<PoolFormState | null>(null);
  const [guacamole, setGuacamole] = React.useState<GuacamoleStatus | null>(null);
  const [guacamoleForm, setGuacamoleForm] = React.useState<GuacamoleFormState | null>(null);
  const [loading, setLoading] = React.useState(true);
  const [busy, setBusy] = React.useState(false);
  const [error, setError] = React.useState<string | null>(null);
  const [message, setMessage] = React.useState<string | null>(null);

  const applyPoolToForm = React.useCallback((p: PoolStatus) => {
    setPoolForm({
      replicas: String(p.replicas ?? p.desired ?? 0),
      minReady: String(p.minReady ?? 0),
      recyclePolicy: p.recyclePolicy || 'Delete',
      createConnections: !!p.createConnections,
      powerEnabled: !!p.powerManagement?.enabled,
      idleSeconds: String(p.powerManagement?.idleSeconds ?? 900),
    });
  }, []);

  const applyGuacamoleToForm = React.useCallback((g: GuacamoleStatus) => {
    setGuacamoleForm({
      replicas: String(g.replicas ?? 1),
      guacdReplicas: String(g.guacdReplicas ?? 1),
      logLevel: g.logLevel || 'info',
      routeEnabled: g.routeEnabled !== false,
      openIDEnabled: !!g.openID?.enabled,
      usernameClaimType: g.openID?.usernameClaimType || 'preferred_username',
      scope: g.openID?.scope || 'openid email profile',
      extensionPriority: g.openID?.extensionPriority || '*,openid',
    });
  }, []);

  const existingSubjects = React.useMemo(() => {
    const set = new Set<string>();
    sessions.forEach((s) => {
      const subject = s.spec?.requester?.subject;
      if (subject) set.add(subject);
    });
    return set;
  }, [sessions]);

  const selectedUsernames = React.useMemo(
    () => Object.keys(selected).filter((u) => selected[u]),
    [selected],
  );

  const selectedSessionNames = React.useMemo(
    () => Object.keys(selectedSessions).filter((n) => selectedSessions[n]),
    [selectedSessions],
  );

  const selectableUsers = React.useMemo(
    () => users.filter((u) => u.enabled && !existingSubjects.has(u.username)),
    [users, existingSubjects],
  );

  const load = React.useCallback(async () => {
    setError(null);
    try {
      const pluginName = 'guacamole-desktop-portal';
      const base = proxyBase(pluginName);
      // consoleFetchJSON attaches OpenShift CSRF headers required by the console proxy.
      const cfg = (await consoleFetchJSON(`${base}/config`)) as PortalConfig;
      setConfig(cfg);

      try {
        const items = (await consoleFetchJSON(`${base}/sessions`)) as SessionItem[];
        setSessions(items || []);
        setSelectedSessions({});
      } catch {
        setSessions([]);
        setSelectedSessions({});
      }

      try {
        const poolStatus = (await consoleFetchJSON(`${base}/pool`)) as PoolStatus;
        setPool(poolStatus);
        applyPoolToForm(poolStatus);
      } catch {
        setPool(null);
        setPoolForm(null);
      }

      try {
        const guac = (await consoleFetchJSON(`${base}/guacamole`)) as GuacamoleStatus;
        setGuacamole(guac);
        applyGuacamoleToForm(guac);
      } catch {
        setGuacamole(null);
        setGuacamoleForm(null);
      }
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setLoading(false);
    }
  }, [applyPoolToForm, applyGuacamoleToForm]);

  React.useEffect(() => {
    void load();
  }, [load]);

  const searchUsers = async () => {
    if (!config) return;
    setBusy(true);
    setError(null);
    try {
      const base = proxyBase(config.pluginName || 'guacamole-desktop-portal');
      const qs = search ? `?search=${encodeURIComponent(search)}` : '';
      const items = (await consoleFetchJSON(`${base}/users${qs}`)) as KeycloakUser[];
      setUsers(items || []);
      setSelected({});
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  };

  const toggleUser = (username: string, checked: boolean) => {
    setSelected((prev) => {
      const next = { ...prev };
      if (checked) {
        next[username] = true;
      } else {
        delete next[username];
      }
      return next;
    });
  };

  const selectAllVisible = (checked: boolean) => {
    if (!checked) {
      setSelected({});
      return;
    }
    const next: Record<string, boolean> = {};
    selectableUsers.forEach((u) => {
      next[u.username] = true;
    });
    setSelected(next);
  };

  const createSessions = async () => {
    if (!config || selectedUsernames.length === 0) return;
    setBusy(true);
    setError(null);
    setMessage(null);
    try {
      const base = proxyBase(config.pluginName || 'guacamole-desktop-portal');
      const resp = (await consoleFetchJSON.post(`${base}/sessions/batch`, {
        subjects: selectedUsernames,
        poolName: config.poolName,
      })) as BatchResponse;

      const parts: string[] = [];
      if (resp.created) parts.push(`${resp.created} created`);
      if (resp.exists) parts.push(`${resp.exists} already existed`);
      if (resp.errors) parts.push(`${resp.errors} failed`);
      setMessage(
        parts.length
          ? `Batch result: ${parts.join(', ')}.`
          : 'Batch completed with no changes.',
      );

      const failed = (resp.results || []).filter((r) => r.status === 'error');
      if (failed.length) {
        setError(
          failed
            .map((r) => `${r.subject}: ${r.error || 'unknown error'}`)
            .join('; '),
        );
      }

      setSelected({});
      await load();
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  };

  const toggleSession = (name: string, checked: boolean) => {
    setSelectedSessions((prev) => {
      const next = { ...prev };
      if (checked) {
        next[name] = true;
      } else {
        delete next[name];
      }
      return next;
    });
  };

  const selectAllSessions = (checked: boolean) => {
    if (!checked) {
      setSelectedSessions({});
      return;
    }
    const next: Record<string, boolean> = {};
    sessions.forEach((s) => {
      const name = s.metadata?.name;
      if (name) next[name] = true;
    });
    setSelectedSessions(next);
  };

  const saveGuacamoleConfig = async () => {
    if (!config || !guacamoleForm) return;
    const replicas = Number(guacamoleForm.replicas);
    const guacdReplicas = Number(guacamoleForm.guacdReplicas);
    if (!Number.isInteger(replicas) || replicas < 1) {
      setError('Guacamole replicas must be an integer >= 1');
      return;
    }
    if (!Number.isInteger(guacdReplicas) || guacdReplicas < 1) {
      setError('guacd replicas must be an integer >= 1');
      return;
    }

    setBusy(true);
    setError(null);
    setMessage(null);
    try {
      const base = proxyBase(config.pluginName || 'guacamole-desktop-portal');
      const body: Record<string, unknown> = {
        replicas,
        guacdReplicas,
        logLevel: guacamoleForm.logLevel,
        routeEnabled: guacamoleForm.routeEnabled,
      };
      if (guacamole?.openID?.configured) {
        body.openID = {
          enabled: guacamoleForm.openIDEnabled,
          usernameClaimType: guacamoleForm.usernameClaimType,
          scope: guacamoleForm.scope,
          extensionPriority: guacamoleForm.extensionPriority,
        };
      }
      const updated = (await consoleFetchJSON.put(`${base}/guacamole`, body)) as GuacamoleStatus;
      setGuacamole(updated);
      applyGuacamoleToForm(updated);
      setMessage('Guacamole configuration saved.');
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  };

  const savePoolConfig = async () => {
    if (!config || !poolForm) return;
    const replicas = Number(poolForm.replicas);
    const minReady = Number(poolForm.minReady);
    const idleSeconds = Number(poolForm.idleSeconds);
    if (!Number.isInteger(replicas) || replicas < 0) {
      setError('Replicas must be an integer >= 0');
      return;
    }
    if (!Number.isInteger(minReady) || minReady < 0) {
      setError('minReady must be an integer >= 0');
      return;
    }
    if (!Number.isInteger(idleSeconds) || idleSeconds < 0) {
      setError('Idle seconds must be an integer >= 0');
      return;
    }
    if (poolForm.recyclePolicy !== 'Delete' && poolForm.recyclePolicy !== 'Retain') {
      setError('Recycle policy must be Delete or Retain');
      return;
    }

    setBusy(true);
    setError(null);
    setMessage(null);
    try {
      const base = proxyBase(config.pluginName || 'guacamole-desktop-portal');
      const updated = (await consoleFetchJSON.put(`${base}/pool`, {
        replicas,
        minReady,
        recyclePolicy: poolForm.recyclePolicy,
        createConnections: poolForm.createConnections,
        powerManagement: {
          enabled: poolForm.powerEnabled,
          idleSeconds,
        },
      })) as PoolStatus;
      setPool(updated);
      applyPoolToForm(updated);
      setMessage('DesktopPool configuration saved.');
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  };

  const wakePool = async () => {
    if (!config) return;
    setBusy(true);
    setError(null);
    setMessage(null);
    try {
      const base = proxyBase(config.pluginName || 'guacamole-desktop-portal');
      await consoleFetchJSON.post(`${base}/pool/wake`, {});
      setMessage('Wake requested — stopped desktops will boot shortly.');
      await load();
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  };

  const suspendPool = async () => {
    if (!config) return;
    const confirmed = window.confirm(
      'Suspend idle Available desktops now? Allocated sessions are never stopped. minReady warm desktops are kept running.',
    );
    if (!confirmed) return;
    setBusy(true);
    setError(null);
    setMessage(null);
    try {
      const base = proxyBase(config.pluginName || 'guacamole-desktop-portal');
      await consoleFetchJSON.post(`${base}/pool/suspend`, {});
      setMessage('Suspend requested — idle Available desktops will power off.');
      await load();
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  };

  const deleteSessions = async () => {
    if (!config || selectedSessionNames.length === 0) return;
    const confirmed = window.confirm(
      `Delete ${selectedSessionNames.length} DesktopSession(s)? Assigned desktops will be released according to the pool recycle policy.`,
    );
    if (!confirmed) return;

    setBusy(true);
    setError(null);
    setMessage(null);
    try {
      const base = proxyBase(config.pluginName || 'guacamole-desktop-portal');
      const resp = (await consoleFetchJSON.post(`${base}/sessions/batch-delete`, {
        names: selectedSessionNames,
      })) as BatchResponse;

      const parts: string[] = [];
      if (resp.deleted) parts.push(`${resp.deleted} deleted`);
      if (resp.errors) parts.push(`${resp.errors} failed`);
      setMessage(parts.length ? `Delete result: ${parts.join(', ')}.` : 'Delete completed.');

      const failed = (resp.results || []).filter((r) => r.status === 'error');
      if (failed.length) {
        setError(
          failed
            .map((r) => `${r.name || r.subject}: ${r.error || 'unknown error'}`)
            .join('; '),
        );
      }

      setSelectedSessions({});
      await load();
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  };

  if (loading) {
    return (
      <PageSection>
        <Bullseye>
          <Spinner />
        </Bullseye>
      </PageSection>
    );
  }

  const allSelectableChecked =
    selectableUsers.length > 0 && selectedUsernames.length === selectableUsers.length;

  return (
    <>
      <PageSection>
        <Title headingLevel="h1">{config?.displayName || 'Desktop Sessions'}</Title>
        <p>
          Allocate desktops from pool <strong>{config?.poolName}</strong> in namespace{' '}
          <strong>{config?.sessionNamespace}</strong> for one or more Keycloak users.
        </p>
      </PageSection>
      {pool && poolForm && (
        <PageSection>
          <Title headingLevel="h2">Desktop Pool</Title>
          <p style={{ marginBottom: 8 }}>
            Phase <strong>{pool.phase || '—'}</strong>
            {' · '}
            Desired <strong>{pool.desired}</strong>
            {' · '}
            Available <strong>{pool.available}</strong>
            {' · '}
            Stopped <strong>{pool.stopped}</strong>
            {' · '}
            Allocated <strong>{pool.allocated}</strong>
            {' · '}
            Booting/Provisioning <strong>{pool.provisioning}</strong>
            {pool.failed ? (
              <>
                {' · '}
                Failed <strong>{pool.failed}</strong>
              </>
            ) : null}
          </p>
          <p style={{ marginBottom: 16 }}>
            Power:{' '}
            <strong>{pool.powerManagement.enabled ? 'enabled' : 'disabled'}</strong>
            {pool.powerManagement.enabled ? (
              <>
                {' '}
                · idle <strong>{formatIdle(pool.powerManagement.idleSeconds)}</strong>
                {' '}
                · minReady <strong>{pool.minReady}</strong>
              </>
            ) : null}
          </p>

          <Title headingLevel="h3">Configure pool</Title>
          <Form
            onSubmit={(e) => {
              e.preventDefault();
              void savePoolConfig();
            }}
            style={{ maxWidth: 640, marginBottom: 16 }}
          >
            <FormGroup label="Replicas" fieldId="pool-replicas">
              <TextInput
                id="pool-replicas"
                type="number"
                min={0}
                value={poolForm.replicas}
                isDisabled={busy}
                onChange={(_e, v) => setPoolForm((f) => (f ? { ...f, replicas: v } : f))}
              />
            </FormGroup>
            <FormGroup label="Min ready (warm floor)" fieldId="pool-minready">
              <TextInput
                id="pool-minready"
                type="number"
                min={0}
                value={poolForm.minReady}
                isDisabled={busy}
                onChange={(_e, v) => setPoolForm((f) => (f ? { ...f, minReady: v } : f))}
              />
            </FormGroup>
            <FormGroup label="Recycle policy" fieldId="pool-recycle">
              <select
                id="pool-recycle"
                value={poolForm.recyclePolicy}
                disabled={busy}
                onChange={(e) =>
                  setPoolForm((f) => (f ? { ...f, recyclePolicy: e.target.value } : f))
                }
                style={{ width: '100%', padding: '6px 8px' }}
              >
                <option value="Delete">Delete (destroy VM on release)</option>
                <option value="Retain">Retain (return VM to Available)</option>
              </select>
            </FormGroup>
            <FormGroup fieldId="pool-create-connections">
              <Checkbox
                id="pool-create-connections"
                label="Create GuacamoleConnection for every Available desktop"
                isChecked={poolForm.createConnections}
                isDisabled={busy}
                onChange={(_e, checked) =>
                  setPoolForm((f) => (f ? { ...f, createConnections: checked } : f))
                }
              />
            </FormGroup>
            <FormGroup fieldId="pool-power-enabled">
              <Checkbox
                id="pool-power-enabled"
                label="Enable power management (idle stop / wake on demand)"
                isChecked={poolForm.powerEnabled}
                isDisabled={busy}
                onChange={(_e, checked) =>
                  setPoolForm((f) => (f ? { ...f, powerEnabled: checked } : f))
                }
              />
            </FormGroup>
            <FormGroup label="Idle seconds before stop" fieldId="pool-idle">
              <TextInput
                id="pool-idle"
                type="number"
                min={0}
                value={poolForm.idleSeconds}
                isDisabled={busy || !poolForm.powerEnabled}
                onChange={(_e, v) => setPoolForm((f) => (f ? { ...f, idleSeconds: v } : f))}
              />
            </FormGroup>
            <div style={{ display: 'flex', gap: 8, marginTop: 8 }}>
              <Button type="submit" variant="primary" isDisabled={busy}>
                Save pool config
              </Button>
              <Button
                variant="secondary"
                onClick={() => applyPoolToForm(pool)}
                isDisabled={busy}
              >
                Reset form
              </Button>
            </div>
          </Form>

          <div style={{ display: 'flex', gap: 8 }}>
            <Button
              variant="secondary"
              onClick={() => void wakePool()}
              isDisabled={busy || !pool.powerManagement.enabled || pool.stopped === 0}
            >
              Wake pool
            </Button>
            <Button
              variant="secondary"
              onClick={() => void suspendPool()}
              isDisabled={busy || !pool.powerManagement.enabled || pool.available === 0}
            >
              Suspend idle
            </Button>
          </div>
        </PageSection>
      )}
      {guacamole && guacamoleForm && (
        <PageSection>
          <Title headingLevel="h2">Guacamole instance</Title>
          <p style={{ marginBottom: 8 }}>
            <strong>
              {guacamole.namespace}/{guacamole.name}
            </strong>
            {' · '}
            Phase <strong>{guacamole.phase || '—'}</strong>
            {guacamole.routeURL ? (
              <>
                {' · '}
                <a href={guacamole.routeURL} target="_blank" rel="noreferrer">
                  Open Guacamole
                </a>
              </>
            ) : null}
          </p>
          {guacamole.openID?.configured ? (
            <p style={{ marginBottom: 16 }}>
              OpenID: <strong>{guacamole.openID.enabled ? 'enabled' : 'disabled'}</strong>
              {guacamole.openID.issuer ? (
                <>
                  {' '}
                  · issuer <code>{guacamole.openID.issuer}</code>
                </>
              ) : null}
              {guacamole.openID.clientID ? (
                <>
                  {' '}
                  · client <code>{guacamole.openID.clientID}</code>
                </>
              ) : null}
            </p>
          ) : (
            <p style={{ marginBottom: 16 }}>OpenID is not configured on this Guacamole CR.</p>
          )}

          <Title headingLevel="h3">Configure Guacamole</Title>
          <Form
            onSubmit={(e) => {
              e.preventDefault();
              void saveGuacamoleConfig();
            }}
            style={{ maxWidth: 640, marginBottom: 16 }}
          >
            <FormGroup label="Web replicas" fieldId="guac-replicas">
              <TextInput
                id="guac-replicas"
                type="number"
                min={1}
                value={guacamoleForm.replicas}
                isDisabled={busy}
                onChange={(_e, v) => setGuacamoleForm((f) => (f ? { ...f, replicas: v } : f))}
              />
            </FormGroup>
            <FormGroup label="guacd replicas" fieldId="guacd-replicas">
              <TextInput
                id="guacd-replicas"
                type="number"
                min={1}
                value={guacamoleForm.guacdReplicas}
                isDisabled={busy}
                onChange={(_e, v) => setGuacamoleForm((f) => (f ? { ...f, guacdReplicas: v } : f))}
              />
            </FormGroup>
            <FormGroup label="Log level" fieldId="guac-log">
              <select
                id="guac-log"
                value={guacamoleForm.logLevel}
                disabled={busy}
                onChange={(e) =>
                  setGuacamoleForm((f) => (f ? { ...f, logLevel: e.target.value } : f))
                }
                style={{ width: '100%', padding: '6px 8px' }}
              >
                <option value="debug">debug</option>
                <option value="info">info</option>
                <option value="warn">warn</option>
                <option value="error">error</option>
              </select>
            </FormGroup>
            <FormGroup fieldId="guac-route">
              <Checkbox
                id="guac-route"
                label="Expose OpenShift Route"
                isChecked={guacamoleForm.routeEnabled}
                isDisabled={busy}
                onChange={(_e, checked) =>
                  setGuacamoleForm((f) => (f ? { ...f, routeEnabled: checked } : f))
                }
              />
            </FormGroup>
            {guacamole.openID?.configured && (
              <>
                <FormGroup fieldId="guac-oidc-enabled">
                  <Checkbox
                    id="guac-oidc-enabled"
                    label="Enable OpenID SSO"
                    isChecked={guacamoleForm.openIDEnabled}
                    isDisabled={busy}
                    onChange={(_e, checked) =>
                      setGuacamoleForm((f) => (f ? { ...f, openIDEnabled: checked } : f))
                    }
                  />
                </FormGroup>
                <FormGroup label="Username claim" fieldId="guac-oidc-claim">
                  <TextInput
                    id="guac-oidc-claim"
                    value={guacamoleForm.usernameClaimType}
                    isDisabled={busy || !guacamoleForm.openIDEnabled}
                    onChange={(_e, v) =>
                      setGuacamoleForm((f) => (f ? { ...f, usernameClaimType: v } : f))
                    }
                  />
                </FormGroup>
                <FormGroup label="OpenID scope" fieldId="guac-oidc-scope">
                  <TextInput
                    id="guac-oidc-scope"
                    value={guacamoleForm.scope}
                    isDisabled={busy || !guacamoleForm.openIDEnabled}
                    onChange={(_e, v) => setGuacamoleForm((f) => (f ? { ...f, scope: v } : f))}
                  />
                </FormGroup>
                <FormGroup label="Extension priority (login UX)" fieldId="guac-oidc-priority">
                  <select
                    id="guac-oidc-priority"
                    value={guacamoleForm.extensionPriority}
                    disabled={busy || !guacamoleForm.openIDEnabled}
                    onChange={(e) =>
                      setGuacamoleForm((f) =>
                        f ? { ...f, extensionPriority: e.target.value } : f,
                      )
                    }
                    style={{ width: '100%', padding: '6px 8px' }}
                  >
                    <option value="*,openid">Show Guacamole form + OpenID link (*,openid)</option>
                    <option value="openid">Auto-redirect to IdP (openid)</option>
                  </select>
                </FormGroup>
              </>
            )}
            <div style={{ display: 'flex', gap: 8, marginTop: 8 }}>
              <Button type="submit" variant="primary" isDisabled={busy}>
                Save Guacamole config
              </Button>
              <Button
                variant="secondary"
                onClick={() => applyGuacamoleToForm(guacamole)}
                isDisabled={busy}
              >
                Reset form
              </Button>
            </div>
          </Form>
        </PageSection>
      )}
      <PageSection>
        {error && (
          <Alert variant={AlertVariant.danger} title="Error" isInline style={{ marginBottom: 16 }}>
            {error}
          </Alert>
        )}
        {message && (
          <Alert variant={AlertVariant.success} title="Success" isInline style={{ marginBottom: 16 }}>
            {message}
          </Alert>
        )}
        <Form
          onSubmit={(e) => {
            e.preventDefault();
            void searchUsers();
          }}
        >
          <FormGroup label="Find Keycloak users" fieldId="user-search">
            <div style={{ display: 'flex', gap: 8 }}>
              <TextInput
                id="user-search"
                value={search}
                onChange={(_event, value) => setSearch(value)}
                placeholder="username, email, or name"
              />
              <Button type="submit" isDisabled={busy}>
                Search
              </Button>
            </div>
          </FormGroup>

          <FormGroup label="Users" fieldId="user-multi-select">
            {users.length === 0 ? (
              <EmptyState>
                <EmptyStateBody>Search Keycloak to list users for batch allocation.</EmptyStateBody>
              </EmptyState>
            ) : (
              <div
                id="user-multi-select"
                style={{
                  border: '1px solid var(--pf-v5-global--BorderColor--100, #d2d2d2)',
                  borderRadius: 4,
                  padding: 12,
                  maxHeight: 320,
                  overflow: 'auto',
                }}
              >
                <div style={{ marginBottom: 8 }}>
                  <Checkbox
                    id="select-all-users"
                    label={`Select all available (${selectableUsers.length})`}
                    isChecked={allSelectableChecked}
                    isDisabled={selectableUsers.length === 0 || busy}
                    onChange={(_event, checked) => selectAllVisible(checked)}
                  />
                </div>
                {users.map((u) => {
                  const hasSession = existingSubjects.has(u.username);
                  const disabled = !u.enabled || hasSession || busy;
                  const label = [
                    u.username,
                    u.email ? `<${u.email}>` : '',
                    !u.enabled ? '(disabled)' : '',
                    hasSession ? '(session exists)' : '',
                  ]
                    .filter(Boolean)
                    .join(' ');
                  return (
                    <div key={u.id} style={{ marginBottom: 4 }}>
                      <Checkbox
                        id={`user-${u.id}`}
                        label={label}
                        isChecked={!!selected[u.username]}
                        isDisabled={disabled}
                        onChange={(_event, checked) => toggleUser(u.username, checked)}
                      />
                    </div>
                  );
                })}
              </div>
            )}
          </FormGroup>

          <Button
            variant="primary"
            onClick={() => void createSessions()}
            isDisabled={selectedUsernames.length === 0 || busy}
          >
            Create Desktop Sessions
            {selectedUsernames.length > 0 ? ` (${selectedUsernames.length})` : ''}
          </Button>
        </Form>
      </PageSection>
      <PageSection>
        <Title headingLevel="h2">Existing sessions</Title>
        {sessions.length === 0 ? (
          <EmptyState>
            <EmptyStateBody>No DesktopSessions in {config?.sessionNamespace}.</EmptyStateBody>
          </EmptyState>
        ) : (
          <>
            <div style={{ display: 'flex', gap: 8, marginTop: 12, marginBottom: 8, alignItems: 'center' }}>
              <Checkbox
                id="select-all-sessions"
                label={`Select all (${sessions.length})`}
                isChecked={
                  sessions.length > 0 && selectedSessionNames.length === sessions.length
                }
                isDisabled={busy}
                onChange={(_event, checked) => selectAllSessions(checked)}
              />
              <Button
                variant="danger"
                onClick={() => void deleteSessions()}
                isDisabled={selectedSessionNames.length === 0 || busy}
              >
                Delete selected
                {selectedSessionNames.length > 0 ? ` (${selectedSessionNames.length})` : ''}
              </Button>
            </div>
            <table className="pf-v5-c-table pf-m-compact" style={{ width: '100%' }}>
              <thead>
                <tr>
                  <th style={{ width: 40 }} />
                  <th>Name</th>
                  <th>Subject</th>
                  <th>Phase</th>
                  <th>Queue</th>
                  <th>Desktop</th>
                  <th>Message</th>
                </tr>
              </thead>
              <tbody>
                {sessions.map((s) => {
                  const name = s.metadata?.name || '';
                  const queue =
                    s.status?.queuePosition != null
                      ? `${s.status.queuePosition}/${s.status.queueLength || '?'}`
                      : '—';
                  return (
                    <tr key={name}>
                      <td>
                        <Checkbox
                          id={`session-${name}`}
                          aria-label={`Select session ${name}`}
                          isChecked={!!selectedSessions[name]}
                          isDisabled={!name || busy}
                          onChange={(_event, checked) => toggleSession(name, checked)}
                        />
                      </td>
                      <td>{name}</td>
                      <td>{s.spec?.requester?.subject}</td>
                      <td>{s.status?.phase || 'Pending'}</td>
                      <td>{queue}</td>
                      <td>{s.status?.desktopName || '—'}</td>
                      <td>{s.status?.message || '—'}</td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </>
        )}
        <div style={{ marginTop: 16 }}>
          <Button variant="secondary" onClick={() => void load()} isDisabled={busy}>
            Refresh
          </Button>
        </div>
      </PageSection>
    </>
  );
};

export default DesktopSessionsPage;
