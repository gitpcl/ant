<?php

namespace App\Services;

// Notifier references the LIVE config key via config('services.mailgun'), so the
// detector's cross-file usage check finds it and leaves that key alone. It never
// references the legacy pinger key, so that one is the genuine orphan the detector
// flags — proving the discrimination (a referenced key is kept, an orphan removed).
class Notifier
{
    public function domain(): string
    {
        return config('services.mailgun')['domain'];
    }
}
