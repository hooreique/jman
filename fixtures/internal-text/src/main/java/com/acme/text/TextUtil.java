package com.acme.text;

import java.util.Locale;

public final class TextUtil {
    public static String normalize(String value) {
        return value.strip().toUpperCase(Locale.ROOT);
    }
}
