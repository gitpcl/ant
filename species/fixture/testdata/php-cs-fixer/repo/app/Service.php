<?php

namespace App;

// ant:php-cs-fixer the class below has trailing-whitespace drift the formatter fixes
class Service
{
    public function handle(string $name): string
    {
        return strtoupper($name);   
    }
}
