import * as React from 'react';
import {
  Alert,
  AlertVariant,
  Bullseye,
  Button,
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
  spec?: { requester?: { subject?: string }; poolRef?: { name?: string } };
  status?: { phase?: string; desktopName?: string; connectionName?: string };
};

const proxyBase = (pluginName: string) =>
  `/api/proxy/plugin/${pluginName}/portal-api`;

const DesktopSessionsPage: React.FC = () => {
  const [config, setConfig] = React.useState<PortalConfig | null>(null);
  const [search, setSearch] = React.useState('');
  const [users, setUsers] = React.useState<KeycloakUser[]>([]);
  const [selected, setSelected] = React.useState('');
  const [sessions, setSessions] = React.useState<SessionItem[]>([]);
  const [loading, setLoading] = React.useState(true);
  const [busy, setBusy] = React.useState(false);
  const [error, setError] = React.useState<string | null>(null);
  const [message, setMessage] = React.useState<string | null>(null);

  const load = React.useCallback(async () => {
    setError(null);
    try {
      // Plugin name is fixed in ConsolePlugin CR.
      const pluginName = 'guacamole-desktop-portal';
      const base = proxyBase(pluginName);
      const cfgRes = await fetch(`${base}/config`);
      if (!cfgRes.ok) {
        throw new Error(`config: ${cfgRes.status} ${await cfgRes.text()}`);
      }
      const cfg = (await cfgRes.json()) as PortalConfig;
      setConfig(cfg);

      const sessRes = await fetch(`${base}/sessions`);
      if (sessRes.ok) {
        setSessions((await sessRes.json()) as SessionItem[]);
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
      const res = await fetch(`${base}/users${qs}`);
      if (!res.ok) {
        throw new Error(`users: ${res.status} ${await res.text()}`);
      }
      setUsers((await res.json()) as KeycloakUser[]);
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  };

  const createSession = async () => {
    if (!config || !selected) return;
    setBusy(true);
    setError(null);
    setMessage(null);
    try {
      const base = proxyBase(config.pluginName || 'guacamole-desktop-portal');
      const res = await fetch(`${base}/sessions`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ subject: selected, poolName: config.poolName }),
      });
      if (!res.ok) {
        throw new Error(`create session: ${res.status} ${await res.text()}`);
      }
      const created = await res.json();
      setMessage(`DesktopSession ${created?.metadata?.name || ''} created for ${selected}`);
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

  return (
    <>
      <PageSection>
        <Title headingLevel="h1">{config?.displayName || 'Desktop Sessions'}</Title>
        <p>
          Allocate a desktop from pool <strong>{config?.poolName}</strong> in namespace{' '}
          <strong>{config?.sessionNamespace}</strong> for a Keycloak user.
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
          <FormGroup label="Find Keycloak user" fieldId="user-search">
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
          <FormGroup label="User" fieldId="user-select">
            <select
              id="user-select"
              value={selected}
              onChange={(e) => setSelected(e.target.value)}
              style={{ minWidth: 320, padding: 8 }}
            >
              <option value="">Select a user…</option>
              {users.map((u) => (
                <option key={u.id} value={u.username} disabled={!u.enabled}>
                  {u.username}
                  {u.email ? ` <${u.email}>` : ''}
                </option>
              ))}
            </select>
          </FormGroup>
          <Button variant="primary" onClick={() => void createSession()} isDisabled={!selected || busy}>
            Create Desktop Session
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
          <table className="pf-v5-c-table pf-m-compact" style={{ width: '100%', marginTop: 16 }}>
            <thead>
              <tr>
                <th>Name</th>
                <th>Subject</th>
                <th>Phase</th>
                <th>Desktop</th>
              </tr>
            </thead>
            <tbody>
              {sessions.map((s) => (
                <tr key={s.metadata?.name}>
                  <td>{s.metadata?.name}</td>
                  <td>{s.spec?.requester?.subject}</td>
                  <td>{s.status?.phase || 'Pending'}</td>
                  <td>{s.status?.desktopName || '—'}</td>
                </tr>
              ))}
            </tbody>
          </table>
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
