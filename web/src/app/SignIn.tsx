import { useState } from 'react';
import { Alert, Button, Center, Paper, Stack, Text, TextInput, Title } from '@mantine/core';
import { useTranslation } from 'react-i18next';
import { startAuthentication, startRegistration } from '@simplewebauthn/browser';
import { errorText } from '../lib/notify';
import { login as signInByPasskey, pairCode, rite, signedIn } from '../lib/signin';

// SignIn is the page of a device not signed in: a link pairs it by a new passkey or one it holds, a paired one signs in
// by its passkey, and the page opens the route after the code; over plain http it says where deck signs in.
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
    const b = await rite('pair/begin', { code: pairCode() });
    try {
      const credential = await startRegistration({ optionsJSON: b.options.publicKey });
      await rite('pair/finish', { ceremony: b.ceremony, name: name.trim(), credential });
    } catch (e) {
      // The browser holds a passkey of deck already, as one synced to it: it pairs by that one.
      if ((e as { code?: string }).code !== 'ERROR_AUTHENTICATOR_PREVIOUSLY_REGISTERED') throw e;
      const held = await rite('pair/begin', { code: pairCode(), held: true });
      const credential = await startAuthentication({ optionsJSON: held.options.publicKey });
      await rite('pair/finish', { ceremony: held.ceremony, name: name.trim(), credential });
    }
  });
  const login = step(signInByPasskey);
  let body;
  if (!secure) {
    body = <Text size="sm" data-signin-where>{why.trim() || t('A browser signs in to deck only over https, or at deck.localhost on the machine deck runs on: open this link there.')}</Text>;
  } else if (pairCode()) {
    body = (
      <>
        <Text size="sm">{t('This link pairs this device with deck by a passkey: a new one made here, or the one of deck this browser already has; the passkey signs the device in from then on.')}</Text>
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
