<?php

namespace App\Services;

// Mailer is a hermetic fixture for laravel-env-call. from() reads env('MAIL_FROM')
// at runtime OUTSIDE config/ — the smell: it returns null once `php artisan
// config:cache` has run. config/mail.php (in this fixture) ALSO calls env(), but
// that call is correct (env() belongs in config/) and the detector's config/
// ignore glob leaves it alone — proving the "outside config/" discrimination.
class Mailer
{
    public function from(): string
    {
        return env('MAIL_FROM');
    }
}
