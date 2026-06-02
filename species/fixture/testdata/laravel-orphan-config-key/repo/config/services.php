<?php

// config/services.php is the hermetic fixture for laravel-orphan-config-key. It
// declares two top-level keys. 'mailgun' is referenced from app/ via
// config('services.mailgun') (a LIVE key — must NOT be removed). 'legacy_pinger'
// is referenced NOWHERE via config('services.legacy_pinger') — an orphan left
// after its consuming code was deleted. The command detector flags the orphan
// line; the delete-match fix removes it; the command verifier re-parses the
// config (php -l + require) to prove it is still a valid PHP array.
return [
    'mailgun' => ['domain' => 'mg.example.test'],
    'legacy_pinger' => ['url' => 'https://pinger.invalid'],
];
