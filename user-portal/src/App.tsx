import * as React from 'react';
import Keycloak from 'keycloak-js';
import {
  Alert,
  Bullseye,
  Button,
  EmptyState,
  EmptyStateBody,
  EmptyStateHeader,
  EmptyStateIcon,
  Form,
  FormGroup,
  Label,
  Page,
  PageSection,
  Spinner,
  Title,
} from '@patternfly/react-core';
import { DesktopIcon } from '@patternfly/react-icons';

type OIDCConfig = {
  url: string;
  realm: string;
  clientId: string;
  issuer?: string;
};

type MeResponse = {
  username: string;
  subject: string;
  groups?: string[];
};

type PoolSummary = {
  name: string;
  namespace: string;
  phase?: string;
  desired: number;
  available: number;
  allocated: number;
};

type SessionView = {
  name: string;
  namespace: string;
  subject?: string;
  poolName?: string;
  phase?: string;
  uxPhase?: string;
  connectionState?: string;
  desktopName?: string;
  queuePosition?: number;
  queueLength?: number;
  message?: string;
  connectURL?: string;
  guacamoleRouteURL?: string;
  releasedReason?: string;
};

let keycloak: Keycloak | null = null;

const loadOIDCConfig = async (): Promise<OIDCConfig> => {
  try {
    const res = await fetch('/config.json', { cache: 'no-store' });
    if (res.ok) {
      return (await res.json()) as OIDCConfig;
    }
  } catch {
    // fall through to API
  }
  const res = await fetch('/api/oidc-config', { cache: 'no-store' });
  if (!res.ok) {
    throw new Error('OIDC config unavailable');
  }
  return (await res.json()) as OIDCConfig;
};

const ensureKeycloak = async (): Promise<Keycloak> => {
  if (keycloak?.authenticated) {
    return keycloak;
  }
  const cfg = await loadOIDCConfig();
  keycloak = new Keycloak({
    url: cfg.url,
    realm: cfg.realm,
    clientId: cfg.clientId,
  });
  const ok = await keycloak.init({
    onLoad: 'login-required',
    pkceMethod: 'S256',
    checkLoginIframe: false,
    scope: 'openid profile email',
    silentCheckSsoRedirectUri: undefined,
  });
  if (!ok) {
    throw new Error('Keycloak login required');
  }
  return keycloak;
};

const api = async <T,>(path: string, init?: RequestInit): Promise<T> => {
  const kc = await ensureKeycloak();
  if (kc.isTokenExpired(30)) {
    await kc.updateToken(60);
  }
  const res = await fetch(path, {
    ...init,
    headers: {
      Accept: 'application/json',
      Authorization: `Bearer ${kc.token}`,
      ...(init?.body ? { 'Content-Type': 'application/json' } : {}),
      ...(init?.headers || {}),
    },
  });
  if (res.status === 204) {
    return undefined as T;
  }
  if (!res.ok) {
    const text = await res.text();
    throw new Error(text || res.statusText);
  }
  return (await res.json()) as T;
};

const uxVariant = (ux?: string): 'green' | 'blue' | 'orange' | 'red' | 'grey' => {
  switch (ux) {
    case 'InUse':
      return 'green';
    case 'Ready':
      return 'blue';
    case 'Disconnected':
      return 'orange';
    case 'Provisioning':
      return 'grey';
    case 'Failed':
      return 'red';
    case 'Released':
      return 'grey';
    default:
      return 'grey';
  }
};

const uxLabel = (ux?: string) => {
  switch (ux) {
    case 'InUse':
      return 'Em uso';
    case 'Ready':
      return 'Pronto';
    case 'Disconnected':
      return 'Desconectado';
    case 'Provisioning':
      return 'Provisionando';
    case 'Failed':
      return 'Falha';
    case 'Released':
      return 'Liberado';
    default:
      return ux || '—';
  }
};

const activeSession = (sessions: SessionView[]) =>
  sessions.find((s) => s.uxPhase !== 'Released' && s.uxPhase !== 'Failed') || null;

const App: React.FC = () => {
  const [me, setMe] = React.useState<MeResponse | null>(null);
  const [pools, setPools] = React.useState<PoolSummary[]>([]);
  const [selectedPool, setSelectedPool] = React.useState('');
  const [session, setSession] = React.useState<SessionView | null>(null);
  const [loading, setLoading] = React.useState(true);
  const [busy, setBusy] = React.useState(false);
  const [error, setError] = React.useState<string | null>(null);

  const refresh = React.useCallback(async (poolName?: string) => {
    setError(null);
    try {
      await ensureKeycloak();
      const meResp = await api<MeResponse>('/api/me');
      setMe(meResp);

      let poolList: PoolSummary[] = [];
      try {
        poolList = (await api<PoolSummary[]>('/api/pools')) || [];
      } catch {
        poolList = [];
      }
      setPools(poolList);

      const pool =
        poolName ||
        selectedPool ||
        poolList[0]?.name ||
        '';
      if (pool && pool !== selectedPool) {
        setSelectedPool(pool);
      }
      const effectivePool = poolName || selectedPool || poolList[0]?.name || '';

      if (!effectivePool) {
        setSession(null);
        return;
      }
      const qs = `?poolName=${encodeURIComponent(effectivePool)}`;
      const sessions = await api<SessionView[]>(`/api/sessions/mine${qs}`);
      setSession(activeSession(sessions));
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setLoading(false);
    }
  }, [selectedPool]);

  React.useEffect(() => {
    void refresh();
    // Initial load only; pool changes call refresh explicitly.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  React.useEffect(() => {
    if (!session) return;
    if (session.uxPhase === 'Released' || session.uxPhase === 'Failed') return;
    const id = window.setInterval(() => {
      void refresh(selectedPool);
    }, 10000);
    return () => window.clearInterval(id);
  }, [session, selectedPool, refresh]);

  const onPoolChange = (name: string) => {
    setSelectedPool(name);
    setSession(null);
    setLoading(true);
    void refresh(name);
  };

  const requestDesktop = async () => {
    if (!selectedPool) {
      setError('Selecione um Desktop Pool');
      return;
    }
    setBusy(true);
    setError(null);
    try {
      const created = await api<SessionView>('/api/sessions/mine', {
        method: 'POST',
        body: JSON.stringify({ poolName: selectedPool }),
      });
      setSession(created);
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  };

  const releaseDesktop = async () => {
    if (!session?.name) return;
    const ok = window.confirm('Liberar seu desktop? A sessão será encerrada.');
    if (!ok) return;
    setBusy(true);
    setError(null);
    try {
      await api(`/api/sessions/mine/${encodeURIComponent(session.name)}`, { method: 'DELETE' });
      setSession(null);
      await refresh(selectedPool);
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  };

  const connect = () => {
    if (session?.connectURL) {
      window.open(session.connectURL, '_blank', 'noopener,noreferrer');
      return;
    }
    if (session?.guacamoleRouteURL) {
      window.open(session.guacamoleRouteURL, '_blank', 'noopener,noreferrer');
    }
  };

  const logout = () => {
    void keycloak?.logout({ redirectUri: window.location.origin + '/' });
  };

  const canConnect =
    !!session &&
    (session.uxPhase === 'Ready' ||
      session.uxPhase === 'InUse' ||
      session.uxPhase === 'Disconnected') &&
    (!!session.connectURL || !!session.guacamoleRouteURL);

  return (
    <Page>
      <PageSection>
        <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', gap: 16 }}>
          <div>
            <Title headingLevel="h1">Meu desktop</Title>
            {me ? (
              <p style={{ marginTop: 8 }}>
                Olá, <strong>{me.subject || me.username}</strong>
              </p>
            ) : null}
          </div>
          {me ? (
            <Button variant="link" onClick={logout}>
              Sair
            </Button>
          ) : null}
        </div>
      </PageSection>
      <PageSection>
        {error ? (
          <Alert variant="danger" title="Erro" isInline style={{ marginBottom: 16 }}>
            {error}
          </Alert>
        ) : null}

        <Form style={{ maxWidth: 480, marginBottom: 24 }}>
          <FormGroup label="Desktop Pool" fieldId="user-selected-pool">
            <select
              id="user-selected-pool"
              value={selectedPool}
              disabled={busy || loading || pools.length === 0}
              onChange={(e) => onPoolChange(e.target.value)}
              style={{ width: '100%', padding: '6px 8px' }}
            >
              {pools.length === 0 ? (
                <option value="">Nenhum pool disponível</option>
              ) : (
                pools.map((p) => (
                  <option key={p.name} value={p.name}>
                    {p.name}
                    {p.phase ? ` (${p.phase})` : ''}
                    {` — disponíveis ${p.available}/${p.desired}`}
                  </option>
                ))
              )}
            </select>
          </FormGroup>
        </Form>

        {loading ? (
          <Bullseye>
            <Spinner />
          </Bullseye>
        ) : !session ? (
          <EmptyState>
            <EmptyStateHeader
              titleText="Nenhum desktop ativo neste pool"
              headingLevel="h2"
              icon={<EmptyStateIcon icon={DesktopIcon} />}
            />
            <EmptyStateBody>
              {selectedPool
                ? `Peça um desktop do pool ${selectedPool}. Quando estiver pronto, você poderá conectar pelo Guacamole.`
                : 'Selecione um Desktop Pool para pedir uma sessão.'}
            </EmptyStateBody>
            <Button
              variant="primary"
              onClick={() => void requestDesktop()}
              isDisabled={busy || !selectedPool}
            >
              Pedir desktop
            </Button>
          </EmptyState>
        ) : (
          <div style={{ maxWidth: 640 }}>
            <p>
              Pool: <strong>{session.poolName || selectedPool}</strong>
            </p>
            <p>
              Status:{' '}
              <Label color={uxVariant(session.uxPhase)}>{uxLabel(session.uxPhase)}</Label>
              {session.connectionState ? (
                <>
                  {' '}
                  · Conexão <strong>{session.connectionState}</strong>
                </>
              ) : null}
            </p>
            <p>
              Sessão: <code>{session.name}</code>
              {session.desktopName ? (
                <>
                  {' '}
                  · Desktop <code>{session.desktopName}</code>
                </>
              ) : null}
            </p>
            {session.queuePosition != null && session.queuePosition > 0 ? (
              <p>
                Fila: posição <strong>{session.queuePosition}</strong>
                {session.queueLength ? <> / {session.queueLength}</> : null}
              </p>
            ) : null}
            {session.message ? <p>{session.message}</p> : null}
            <div style={{ display: 'flex', gap: 8, marginTop: 16, flexWrap: 'wrap' }}>
              <Button variant="primary" onClick={connect} isDisabled={busy || !canConnect}>
                {session.uxPhase === 'Disconnected' ? 'Reconectar' : 'Conectar'}
              </Button>
              <Button variant="secondary" onClick={() => void refresh(selectedPool)} isDisabled={busy}>
                Atualizar
              </Button>
              <Button variant="danger" onClick={() => void releaseDesktop()} isDisabled={busy}>
                Liberar
              </Button>
            </div>
          </div>
        )}
      </PageSection>
    </Page>
  );
};

export default App;
