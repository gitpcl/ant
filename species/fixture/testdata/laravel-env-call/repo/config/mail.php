<?php

// config/mail.php is the CORRECT place to call env(): the detector's config/
// ignore glob excludes this file, so this env() call is NOT flagged. It is the
// non-matching case that proves laravel-env-call only flags env() OUTSIDE config/.
return [
    'from' => env('MAIL_FROM', 'noreply@example.test'),
];
