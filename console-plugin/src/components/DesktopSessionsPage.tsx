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

const proxyBase = (pluginName: string) => `/api/proxy/plugin/${pluginName}/portal-api`;

const DesktopSessionsPage: React.FC = () => {
  const [config, setConfig] = React.useState<PortalConfig | null>(null);
  const [search, setSearch] = React.useState('');
  const [users, setUsers] = React.useState<KeycloakUser[]>([]);
  const [selected, setSelected] = React.useState<Record<string, boolean>>({});
  const [selectedSessions, setSelectedSessions] = React.useState<Record<string, boolean>>({});
  const [sessions, setSessions] = React.useState<SessionItem[]>([]);
  const [loading, setLoading] = React.useState(true);
  const [busy, setBusy] = React.useState(false);
  const [error, setError] = React.useState<string | null>(null);
  const [message, setMessage] = React.useState<string | null>(null);

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
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setLoading(false);
    }
  }, []);

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
