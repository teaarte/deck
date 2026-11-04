import { useState } from 'react';
import { Alert, Button, Center, Paper, Stack, Text, TextInput, Title } from '@mantine/core';
import { useTranslation } from 'react-i18next';
import { startAuthentication, startRegistration } from '@simplewebauthn/browser';
import i18n from '../lib/i18n';
import { errorText } from '../lib/notify';
import { pairCode, signedIn } from '../lib/signin';

interface Begun { ceremony: string; options: { publicKey: never } }

// rite is a step of signing in; deck's refusal throws its word.
async function rite(step: string, body: object): Promise<Begun> {
  const r = await fetch('/auth/' + step, {
    method: 'POST', headers: { 'Content-Type': 'application/json', 'Deck-Lang': i18n.language ?? '' }, body: JSON.stringify(body),
  });
  const text = await r.text();
  if (!r.ok) throw new Error(text.trim() || r.statusText);
  return JSON.parse(text) as Begun;
}

// SignIn is the page of a device not signed in: a link of deck pairs it with a new passkey, its passkey signs it in,
// and the page opens the route after the code; over plain http it says where deck signs in.
export function SignIn({ why }: { why: string }) {
  const { t } = useTranslation();
  // Browsers take deck.localhost to this machine and sign in there as over https.
  const secure = location.protocol === 'https:' || location.hostname.endsWith('.localhost');
  const [name, setName] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const step = (run: () => Promise<void>) => async () => {
    setBusy(true);
    setError('');
    try {
      await run();
      signedIn();
    } catch (e) {
      setError(errorText(e));
      setBusy(false);
    }
  };
  const pair = step(async () => {
    const b = await rite('pair/begin', { code: pairCode });
    const credential = await startRegistration({ optionsJSON: b.options.publicKey });
    await rite('pair/finish', { ceremony: b.ceremony, name: name.trim(), credential });
  });
  const login = step(async () => {
    const b = await rite('login/begin', {});
    const credential = await startAuthentication({ optionsJSON: b.options.publicKey });
    await rite('login/finish', { ceremony: b.ceremony, credential });
  });
  let body;
  if (!secure) {
    body = <Text size="sm" data-signin-where>{why.trim() || t('A browser signs in to deck only over https, or at deck.localhost on the machine deck runs on: open this link there.')}</Text>;
  } else if (pairCode) {
    body = (
      <>
        <Text size="sm">{t('This link pairs this device with deck: it makes a passkey here, and the passkey signs the device in from then on.')}</Text>
        <TextInput label={t('Name of this device')} description={t('Left empty, deck names it by its browser, such as iPhone — Safari.')}
          value={name} onChange={(e) => setName(e.currentTarget.value)} data-signin-name />
        <Button onClick={() => void pair()} loading={busy} data-signin="pair">{t('Pair this device')}</Button>
      </>
    );
  } else {
    body = (
      <>
        <Text size="sm">{t('This device is not signed in to deck.')}</Text>
        <Button onClick={() => void login()} loading={busy} data-signin="passkey">{t('Sign in with a passkey')}</Button>
        <Text size="xs" c="dimmed">{t('A device not paired yet pairs by a link: run deck pair where deck runs, or make one in Settings on a device signed in.')}</Text>
      </>
    );
  }
  return (
    <Center mih="100dvh" p="md">
      <Paper withBorder p="lg" maw={420} w="100%">
        <Stack gap="sm" data-signin-page>
          <Title order={3}>deck</Title>
          {body}
          {error && <Alert color="red" p="xs" data-signin-error>{error}</Alert>}
        </Stack>
      </Paper>
    </Center>
  );
}
