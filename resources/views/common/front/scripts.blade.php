@if( config('0-general.google_analytics') )
<script>
    (function (i, s, o, g, r, a, m) {
        i['GoogleAnalyticsObject'] = r;
        i[r] = i[r] || function () {
                    (i[r].q = i[r].q || []).push(arguments)
                }, i[r].l = 1 * new Date();
        a = s.createElement(o), m = s.getElementsByTagName(o)[0];
        a.async = 1;
        a.src = g;
        m.parentNode.insertBefore(a, m)
    })(window, document, 'script', 'https://www.google-analytics.com/analytics.js', 'ga');
    ga('create', '{{ config("0-general.google_analytics") }}', 'auto' @if(\Auth::id()) ,{ userId: "{{ \Auth::id() ? \Auth::id() : 'Guest' }}" } @endif );
    ga('send', 'pageview');
	ga('set', 'userId', {{ \Auth::user() ? \Auth::id() : 0 }} );
</script>
@endif
@if( config('0-general.crisp') )
<script data-cfasync='false'>
    window.$crisp=[];
    CRISP_RUNTIME_CONFIG = {
      locale : 'fa'
    };
    CRISP_WEBSITE_ID = "{{ config('0-general.crisp') }}";(function(){
          d=document;s=d.createElement('script');
          s.src='https://client.crisp.chat/l.js';
          s.async=1;d.getElementsByTagName('head')[0].appendChild(s);
    })();       
</script>
@endif